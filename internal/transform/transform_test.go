package transform

import (
	"encoding/json"
	"strings"
	"testing"
)

func defaultOpt() Options {
	return Options{
		Enabled:               true,
		InjectThinkTag:        true,
		StripReasoningFields:  true,
		ParseThinkFromContent: true,
		ReasoningFields:       []string{"reasoning_content", "reasoning"},
	}
}

func TestNormalizeNonStreamReasoningKeepsPassthroughFields(t *testing.T) {
	in := []byte(`{
	  "id":"chatcmpl-1",
	  "object":"chat.completion",
	  "created":123,
	  "model":"qwen3.6",
	  "provider_extra":{"trace_id":"abc","nested":{"x":1}},
	  "choices":[{
	    "index":0,
	    "finish_reason":"stop",
	    "logprobs":{"content":null},
	    "message":{
	      "role":"assistant",
	      "content":"答案",
	      "reasoning":"思考",
	      "tool_calls":[{"id":"call_1","type":"function"}],
	      "annotations":[{"a":1}],
	      "extra":{"keep":true}
	    }
	  }],
	  "usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3},
	  "system_fingerprint":"fp_abc"
	}`)
	out, err := NormalizeNonStream(in, defaultOpt())
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatal(err)
	}
	if root["provider_extra"].(map[string]any)["trace_id"] != "abc" {
		t.Fatalf("top-level extra field lost: %s", out)
	}
	if root["system_fingerprint"] != "fp_abc" {
		t.Fatalf("system_fingerprint lost: %s", out)
	}
	choice := root["choices"].([]any)[0].(map[string]any)
	if choice["logprobs"].(map[string]any)["content"] != nil {
		t.Fatalf("choice logprobs changed: %s", out)
	}
	msg := choice["message"].(map[string]any)
	content := msg["content"].(string)
	if !strings.Contains(content, "<think>\n思考\n</think>") {
		t.Fatalf("reasoning not moved to think: %s", out)
	}
	if _, ok := msg["reasoning"]; ok {
		t.Fatalf("consumed reasoning field still exists: %s", out)
	}
	if msg["extra"].(map[string]any)["keep"] != true {
		t.Fatalf("message extra lost: %s", out)
	}
	if len(msg["tool_calls"].([]any)) != 1 {
		t.Fatalf("tool_calls lost: %s", out)
	}
	usage := root["usage"].(map[string]any)
	if usage["total_tokens"].(float64) != 3 {
		t.Fatalf("usage changed: %s", out)
	}
}

func TestNormalizeNonStreamCollectsMultipleConfiguredReasoningFields(t *testing.T) {
	in := []byte(`{"choices":[{"message":{"role":"assistant","content":"答案","reasoning_content":"A","reasoning":"B","extra_reasoning":"C","extra":"keep"}}]}`)
	opt := defaultOpt()
	opt.ReasoningFields = []string{"reasoning_content", "reasoning", "extra_reasoning"}
	out, err := NormalizeNonStream(in, opt)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal(out, &root)
	msg := root["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	content := msg["content"].(string)
	if !strings.Contains(content, "A\nB\nC") {
		t.Fatalf("reasoning fields not collected: %s", out)
	}
	if msg["extra"] != "keep" {
		t.Fatalf("unrelated extra changed: %s", out)
	}
}

func TestFilterRawThinkContentSplitTags(t *testing.T) {
	s := NewStreamState()
	parts := []string{"<think>", "hidden", "</th", "ink>\n", "answer"}
	var out string
	for _, p := range parts {
		out += s.FilterRawThinkContent(p)
	}
	if out != "\nanswer" {
		t.Fatalf("got %q", out)
	}
}

func TestNormalizeSSEReasoningKeepsExtraAndHandlesSameChunkContent(t *testing.T) {
	tracker := NewStreamTracker()
	opt := defaultOpt()
	first, err := NormalizeSSEData([]byte(`{"id":"1","extra":"top","choices":[{"index":0,"finish_reason":null,"delta":{"role":"assistant","reasoning_content":"abc","content":"def","extra":{"keep":true}}}]}`), opt, tracker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(first), "reasoning_content") {
		t.Fatalf("reasoning_content should be consumed: %s", first)
	}
	var root map[string]any
	_ = json.Unmarshal(first, &root)
	if root["extra"] != "top" {
		t.Fatalf("top extra lost: %s", first)
	}
	delta := root["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	if !strings.Contains(delta["content"].(string), "<think>\nabc\n</think>\ndef") {
		t.Fatalf("bad content: %s", first)
	}
	if delta["extra"].(map[string]any)["keep"] != true || delta["role"] != "assistant" {
		t.Fatalf("delta passthrough lost: %s", first)
	}
}

func TestNormalizeSSEUsesSeparateStatePerChoice(t *testing.T) {
	tracker := NewStreamTracker()
	opt := defaultOpt()
	_, err := NormalizeSSEData([]byte(`{"choices":[{"index":0,"delta":{"reasoning":"A"}},{"index":1,"delta":{"reasoning":"B"}}]}`), opt, tracker)
	if err != nil {
		t.Fatal(err)
	}
	out, err := NormalizeSSEData([]byte(`{"choices":[{"index":1,"delta":{"content":"answer1"}},{"index":0,"delta":{"content":"answer0"}}]}`), opt, tracker)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal(out, &root)
	choices := root["choices"].([]any)
	closed := 0
	for _, ch := range choices {
		delta := ch.(map[string]any)["delta"].(map[string]any)
		if strings.Contains(delta["content"].(string), "</think>") {
			closed++
		}
	}
	if closed != 2 {
		t.Fatalf("expected two independent think close tags: %s", out)
	}
}

func TestMarshalNoEscapeKeepsThinkTagsReadable(t *testing.T) {
	in := []byte(`{"choices":[{"message":{"role":"assistant","content":"答案","reasoning":"思考"}}]}`)
	out, err := NormalizeNonStream(in, defaultOpt())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `\u003c`) || strings.Contains(string(out), `\u003e`) {
		t.Fatalf("html escaped think tag: %s", out)
	}
	if !strings.Contains(string(out), `<think>`) || !strings.Contains(string(out), `</think>`) {
		t.Fatalf("missing raw think tag: %s", out)
	}
}

func TestNormalizeRequestReordersSystemMessages(t *testing.T) {
	in := []byte(`{"model":"qwen3.6","messages":[{"role":"user","content":"你好"},{"role":"system","content":"你是助手"},{"role":"assistant","content":"好"}],"stream":true}`)
	out, info, err := NormalizeRequest(in, RequestOptions{Enabled: true, ReorderSystemMessages: true})
	if err != nil {
		t.Fatal(err)
	}
	if !info.SystemMessagesReordered || info.Model != "qwen3.6" || !info.Stream {
		t.Fatalf("bad info: %+v", info)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatal(err)
	}
	msgs := root["messages"].([]any)
	if msgs[0].(map[string]any)["role"] != "system" || msgs[1].(map[string]any)["role"] != "user" {
		t.Fatalf("system message not first: %s", out)
	}
}

func TestApplyRequestPatchesSetsNestedThinkingFlags(t *testing.T) {
	in := []byte(`{"model":"qwen36-direct","messages":[{"role":"user","content":"hi"}]}`)
	out, result, err := ApplyRequestPatches(in, []RequestPatch{
		{Op: "set", Path: "chat_template_kwargs.enable_thinking", Value: false},
		{Op: "set", Path: "enable_thinking", Value: false},
		{Op: "set", Path: "extra_body.enable_thinking", Value: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied != 3 || !result.Changed {
		t.Fatalf("bad patch result: %+v", result)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatal(err)
	}
	if root["enable_thinking"] != false {
		t.Fatalf("top-level enable_thinking not set: %s", out)
	}
	if root["chat_template_kwargs"].(map[string]any)["enable_thinking"] != false {
		t.Fatalf("chat_template_kwargs.enable_thinking not set: %s", out)
	}
	if root["extra_body"].(map[string]any)["enable_thinking"] != false {
		t.Fatalf("extra_body.enable_thinking not set: %s", out)
	}
}

func TestApplyRequestPatchesDelete(t *testing.T) {
	in := []byte(`{"model":"qwen","enable_thinking":true,"chat_template_kwargs":{"enable_thinking":true}}`)
	out, result, err := ApplyRequestPatches(in, []RequestPatch{
		{Op: "delete", Path: "enable_thinking"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied != 1 || !result.Changed {
		t.Fatalf("bad patch result: %+v", result)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["enable_thinking"]; ok {
		t.Fatalf("enable_thinking should be deleted: %s", out)
	}
	if root["chat_template_kwargs"].(map[string]any)["enable_thinking"] != true {
		t.Fatalf("unrelated nested field changed: %s", out)
	}
}
