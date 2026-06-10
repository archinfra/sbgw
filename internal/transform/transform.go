package transform

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	ReasoningContentField = "reasoning_content"
	ReasoningField        = "reasoning"
	ContentField          = "content"
	ThinkOpen             = "<think>"
	ThinkClose            = "</think>"
)

type Options struct {
	Enabled               bool
	InjectThinkTag        bool
	StripReasoningFields  bool
	ParseThinkFromContent bool
	ReasoningFields       []string
}

func (o Options) normalizedReasoningFields() []string {
	if len(o.ReasoningFields) == 0 {
		return []string{ReasoningContentField, ReasoningField}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(o.ReasoningFields))
	for _, f := range o.ReasoningFields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	if len(out) == 0 {
		return []string{ReasoningContentField, ReasoningField}
	}
	return out
}

// NormalizeNonStream only moves configured reasoning fields into content as
// <think>...</think>. All unrelated top-level, choice, message and provider
// extension fields are preserved exactly as JSON values.
func NormalizeNonStream(body []byte, opt Options) ([]byte, error) {
	if !opt.Enabled {
		return body, nil
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body, nil
	}
	choices, ok := root["choices"].([]any)
	if !ok {
		return body, nil
	}
	for _, ch := range choices {
		cm, ok := ch.(map[string]any)
		if !ok {
			continue
		}
		msg, ok := cm["message"].(map[string]any)
		if !ok {
			continue
		}
		normalizeMessageMap(msg, opt)
	}
	return MarshalNoEscape(root)
}

type StreamTracker struct {
	states map[int]*StreamState
}

func NewStreamTracker() *StreamTracker {
	return &StreamTracker{states: map[int]*StreamState{}}
}

func (t *StreamTracker) State(index int) *StreamState {
	if t == nil {
		return NewStreamState()
	}
	s, ok := t.states[index]
	if !ok {
		s = NewStreamState()
		t.states[index] = s
	}
	return s
}

// NormalizeSSEData only rewrites choices[].delta by moving configured reasoning
// fields into delta.content. Unknown fields are not interpreted or removed.
func NormalizeSSEData(data []byte, opt Options, tracker *StreamTracker) ([]byte, error) {
	if !opt.Enabled || tracker == nil {
		return data, nil
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "[DONE]" {
		return data, nil
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return data, nil
	}
	choices, ok := root["choices"].([]any)
	if !ok {
		return data, nil
	}
	for pos, ch := range choices {
		cm, ok := ch.(map[string]any)
		if !ok {
			continue
		}
		delta, ok := cm["delta"].(map[string]any)
		if !ok {
			continue
		}
		idx := choiceIndex(cm, pos)
		normalizeDeltaMap(delta, opt, tracker.State(idx))
	}
	return MarshalNoEscape(root)
}

func normalizeMessageMap(msg map[string]any, opt Options) {
	content, contentOK, contentIsString := getOptionalString(msg, ContentField)
	reasoning, consumedFields := collectReasoning(msg, opt.normalizedReasoningFields())

	if opt.ParseThinkFromContent && reasoning == "" && contentIsString && strings.Contains(content, ThinkOpen) {
		think, answer, ok := ExtractThink(content)
		if ok {
			reasoning = think
			content = answer
			contentOK = true
			contentIsString = true
		}
	}

	if reasoning != "" && opt.InjectThinkTag {
		// Preserve non-string content instead of guessing how to merge it. Chat
		// completion text responses normally use string/null content; for future
		// multimodal extension objects we leave the original content untouched.
		if !contentOK || contentIsString {
			msg[ContentField] = joinThinkAndContent(reasoning, content)
		}
	}
	stripConsumedReasoningFields(msg, opt, consumedFields)
}

func normalizeDeltaMap(delta map[string]any, opt Options, state *StreamState) {
	content, contentOK, contentIsString := getOptionalString(delta, ContentField)
	reasoning, consumedFields := collectReasoning(delta, opt.normalizedReasoningFields())

	var out strings.Builder
	if reasoning != "" {
		if opt.InjectThinkTag {
			if !state.ThinkingStarted {
				out.WriteString(ThinkOpen)
				out.WriteString("\n")
				state.ThinkingStarted = true
			}
			out.WriteString(reasoning)
		} else {
			out.WriteString(reasoning)
		}
	}

	if contentOK && contentIsString && content != "" {
		if opt.ParseThinkFromContent {
			content = state.FilterRawThinkContent(content)
		}
		if content != "" {
			if state.ThinkingStarted && !state.ThinkingClosed {
				out.WriteString("\n")
				out.WriteString(ThinkClose)
				out.WriteString("\n")
				state.ThinkingClosed = true
			}
			out.WriteString(content)
		}
	}

	if out.Len() > 0 {
		delta[ContentField] = out.String()
	} else if contentOK && contentIsString {
		// Keep empty string content chunks as-is, e.g. the initial assistant role
		// chunk commonly emitted by OpenAI-compatible servers.
		delta[ContentField] = content
	}
	stripConsumedReasoningFields(delta, opt, consumedFields)
}

func ExtractThink(s string) (thinking string, answer string, ok bool) {
	start := strings.Index(s, ThinkOpen)
	end := strings.Index(s, ThinkClose)
	if start < 0 || end < 0 || end < start {
		return "", s, false
	}
	thinking = s[start+len(ThinkOpen) : end]
	answer = strings.TrimSpace(s[:start] + s[end+len(ThinkClose):])
	return strings.TrimSpace(thinking), answer, true
}

func joinThinkAndContent(reasoning, content string) string {
	reasoning = strings.TrimSpace(reasoning)
	content = strings.TrimSpace(content)
	if reasoning == "" {
		return content
	}
	if content == "" {
		return ThinkOpen + "\n" + reasoning + "\n" + ThinkClose
	}
	return ThinkOpen + "\n" + reasoning + "\n" + ThinkClose + "\n\n" + content
}

func collectReasoning(m map[string]any, fields []string) (string, []string) {
	parts := make([]string, 0, len(fields))
	consumed := make([]string, 0, len(fields))
	for _, key := range fields {
		v, exists := m[key]
		if !exists {
			continue
		}
		s, ok := v.(string)
		if !ok {
			// Non-string extension values are left untouched.
			continue
		}
		consumed = append(consumed, key)
		if strings.TrimSpace(s) != "" {
			parts = append(parts, s)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n")), consumed
}

func stripConsumedReasoningFields(m map[string]any, opt Options, consumed []string) {
	if !opt.StripReasoningFields {
		return
	}
	for _, key := range consumed {
		delete(m, key)
	}
}

func getOptionalString(m map[string]any, key string) (value string, exists bool, isString bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", ok, true
	}
	s, ok := v.(string)
	if !ok {
		return "", true, false
	}
	return s, true, true
}

func choiceIndex(choice map[string]any, fallback int) int {
	v, ok := choice["index"]
	if !ok {
		return fallback
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return int(i)
		}
	case string:
		var i int
		if _, err := fmt.Sscanf(n, "%d", &i); err == nil {
			return i
		}
	}
	return fallback
}

// DebugSnapshot returns deterministic state labels for tests and diagnostics.
func (t *StreamTracker) DebugSnapshot() []int {
	if t == nil {
		return nil
	}
	keys := make([]int, 0, len(t.states))
	for k := range t.states {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}
