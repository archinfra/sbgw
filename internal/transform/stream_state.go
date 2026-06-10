package transform

import "strings"

type StreamState struct {
	ThinkingStarted bool
	ThinkingClosed  bool
	rawBuf          string
	insideRawThink  bool
}

func NewStreamState() *StreamState { return &StreamState{} }

// FilterRawThinkContent removes raw <think>...</think> tags and their inner text
// from upstreams that stream thinking as normal content. It is intentionally
// conservative and keeps possible partial tags in an internal buffer.
func (s *StreamState) FilterRawThinkContent(chunk string) string {
	if chunk == "" {
		return ""
	}
	s.rawBuf += chunk
	out := strings.Builder{}
	for {
		if s.rawBuf == "" {
			break
		}
		if s.insideRawThink {
			idx := strings.Index(s.rawBuf, ThinkClose)
			if idx < 0 {
				if keep := possibleTagSuffix(s.rawBuf); keep != s.rawBuf {
					s.rawBuf = keep
				}
				break
			}
			s.rawBuf = s.rawBuf[idx+len(ThinkClose):]
			s.insideRawThink = false
			continue
		}
		idx := strings.Index(s.rawBuf, ThinkOpen)
		if idx < 0 {
			keep := possibleTagSuffix(s.rawBuf)
			emitLen := len(s.rawBuf) - len(keep)
			if emitLen > 0 {
				out.WriteString(s.rawBuf[:emitLen])
			}
			s.rawBuf = keep
			break
		}
		out.WriteString(s.rawBuf[:idx])
		s.rawBuf = s.rawBuf[idx+len(ThinkOpen):]
		s.insideRawThink = true
	}
	return out.String()
}

func possibleTagSuffix(s string) string {
	max := len(ThinkOpen)
	if len(ThinkClose) > max {
		max = len(ThinkClose)
	}
	if len(s) < max {
		max = len(s)
	}
	for n := max; n > 0; n-- {
		suf := s[len(s)-n:]
		if strings.HasPrefix(ThinkOpen, suf) || strings.HasPrefix(ThinkClose, suf) {
			return suf
		}
	}
	return ""
}
