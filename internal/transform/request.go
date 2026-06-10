package transform

import "encoding/json"

type RequestOptions struct {
	Enabled               bool
	ReorderSystemMessages bool
}

type RequestInfo struct {
	Model                   string
	Stream                  bool
	SystemMessagesReordered bool
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
