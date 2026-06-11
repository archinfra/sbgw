package transform

import (
	"encoding/json"
	"strings"
)

type RequestOptions struct {
	Enabled               bool
	ReorderSystemMessages bool
}

type RequestInfo struct {
	Model                   string
	Stream                  bool
	SystemMessagesReordered bool
}

type RequestPatch struct {
	Op    string
	Path  string
	Value any
}

type PatchResult struct {
	Changed bool
	Applied int
}

func NormalizeRequest(body []byte, opt RequestOptions) ([]byte, RequestInfo, error) {
	info := RequestInfo{}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body, info, nil
	}
	if m, ok := root["model"].(string); ok {
		info.Model = m
	}
	if stream, ok := root["stream"].(bool); ok {
		info.Stream = stream
	}
	changed := false
	if opt.Enabled && opt.ReorderSystemMessages {
		if msgs, ok := root["messages"].([]any); ok {
			newMsgs, reordered := reorderSystemMessages(msgs)
			if reordered {
				root["messages"] = newMsgs
				info.SystemMessagesReordered = true
				changed = true
			}
		}
	}
	if !changed {
		return body, info, nil
	}
	out, err := MarshalNoEscape(root)
	if err != nil {
		return body, info, nil
	}
	return out, info, nil
}

func RewriteModel(body []byte, upstreamModel string) ([]byte, bool, error) {
	if upstreamModel == "" {
		return body, false, nil
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body, false, nil
	}
	if root["model"] == upstreamModel {
		return body, false, nil
	}
	root["model"] = upstreamModel
	out, err := MarshalNoEscape(root)
	if err != nil {
		return body, false, nil
	}
	return out, true, nil
}

func ApplyRequestPatches(body []byte, patches []RequestPatch) ([]byte, PatchResult, error) {
	res := PatchResult{}
	if len(patches) == 0 {
		return body, res, nil
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body, res, err
	}
	for _, patch := range patches {
		op := strings.TrimSpace(strings.ToLower(patch.Op))
		if op == "" {
			op = "set"
		}
		path := splitPatchPath(patch.Path)
		if len(path) == 0 {
			continue
		}
		switch op {
		case "set":
			if setPatchValue(root, path, patch.Value) {
				res.Changed = true
				res.Applied++
			}
		case "delete":
			if deletePatchValue(root, path) {
				res.Changed = true
				res.Applied++
			}
		}
	}
	if !res.Changed {
		return body, res, nil
	}
	out, err := MarshalNoEscape(root)
	if err != nil {
		return body, res, err
	}
	return out, res, nil
}

func splitPatchPath(path string) []string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, ".")
	if path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func setPatchValue(root map[string]any, path []string, value any) bool {
	cur := root
	for _, key := range path[:len(path)-1] {
		next, ok := cur[key].(map[string]any)
		if !ok || next == nil {
			next = map[string]any{}
			cur[key] = next
		}
		cur = next
	}
	leaf := path[len(path)-1]
	if existing, ok := cur[leaf]; ok && valuesEqual(existing, value) {
		return false
	}
	cur[leaf] = value
	return true
}

func deletePatchValue(root map[string]any, path []string) bool {
	cur := root
	for _, key := range path[:len(path)-1] {
		next, ok := cur[key].(map[string]any)
		if !ok || next == nil {
			return false
		}
		cur = next
	}
	leaf := path[len(path)-1]
	if _, ok := cur[leaf]; !ok {
		return false
	}
	delete(cur, leaf)
	return true
}

func valuesEqual(a, b any) bool {
	ab, aerr := json.Marshal(a)
	bb, berr := json.Marshal(b)
	return aerr == nil && berr == nil && string(ab) == string(bb)
}

func reorderSystemMessages(msgs []any) ([]any, bool) {
	systems := make([]any, 0)
	others := make([]any, 0, len(msgs))
	seenNonSystem := false
	reordered := false
	for _, item := range msgs {
		role := ""
		if m, ok := item.(map[string]any); ok {
			role, _ = m["role"].(string)
		}
		if role == "system" {
			if seenNonSystem {
				reordered = true
			}
			systems = append(systems, item)
			continue
		}
		seenNonSystem = true
		others = append(others, item)
	}
	if !reordered {
		return msgs, false
	}
	out := make([]any, 0, len(msgs))
	out = append(out, systems...)
	out = append(out, others...)
	return out, true
}

func ExtractTotalTokens(body []byte) int64 {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return 0
	}
	usage, ok := root["usage"].(map[string]any)
	if !ok {
		return 0
	}
	return numberToInt64(usage["total_tokens"])
}

func numberToInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}
