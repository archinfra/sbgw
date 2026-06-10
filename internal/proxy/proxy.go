package proxy

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/archinfra/sbgw/internal/config"
	"github.com/archinfra/sbgw/internal/transform"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ChatProxy struct {
	cfg       *config.Config
	log       *zap.Logger
	client    *http.Client
	upstream  *url.URL
	transform transform.Options
}

func NewChatProxy(cfg *config.Config, log *zap.Logger) (*ChatProxy, error) {
	u, err := url.Parse(strings.TrimRight(cfg.Upstream.BaseURL, "/"))
	if err != nil {
		return nil, err
	}
	return &ChatProxy{
		cfg:      cfg,
		log:      log,
		client:   &http.Client{Timeout: cfg.Upstream.Timeout},
		upstream: u,
		transform: transform.Options{
			Enabled:               cfg.Transform.Enabled,
			InjectThinkTag:        cfg.Transform.InjectThinkTag,
			StripReasoningFields:  cfg.Transform.StripReasoningFields,
			ParseThinkFromContent: cfg.Transform.ParseThinkFromContent,
			ReasoningFields:       cfg.Transform.ReasoningFields,
		},
	}, nil
}

func (p *ChatProxy) HandleChatCompletions(c *gin.Context) {
	reqID := c.GetHeader("X-Request-ID")
	if reqID == "" {
		reqID = uuid.NewString()
	}
	c.Set("request_id", reqID)

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "read request body failed"}})
		return
	}
	if p.cfg.Log.LogBody {
		p.log.Info("upstream request", zap.String("request_id", reqID), zap.ByteString("body", limitBody(body, p.cfg.Log.MaxBodySize)))
	}

	target := *p.upstream
	target.Path = strings.TrimRight(target.Path, "/") + "/v1/chat/completions"

	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	copyHeaders(req.Header, c.Request.Header)
	if p.cfg.Upstream.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.Upstream.APIKey)
	} else if !p.cfg.Upstream.ForwardClientAuthorization {
		// Local SK tokens are for gateway access. Do not leak them to upstream by default.
		req.Header.Del("Authorization")
	}
	req.Header.Set("X-Request-ID", reqID)

	start := time.Now()
	resp, err := p.client.Do(req)
	if err != nil {
		p.log.Error("upstream request failed", zap.String("request_id", reqID), zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "upstream request failed", "detail": err.Error()}})
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(c.Writer.Header(), resp.Header)
	c.Writer.Header().Set("X-Request-ID", reqID)
	c.Status(resp.StatusCode)

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		p.handleStream(c, resp.Body, reqID, start)
		return
	}
	p.handleNonStream(c, resp.Body, reqID, start)
}

func (p *ChatProxy) handleNonStream(c *gin.Context, body io.Reader, reqID string, start time.Time) {
	respBody, err := io.ReadAll(body)
	if err != nil {
		p.log.Error("read upstream response failed", zap.String("request_id", reqID), zap.Error(err))
		return
	}
	out, err := transform.NormalizeNonStream(respBody, p.transform)
	if err != nil {
		out = respBody
	}
	if p.cfg.Log.LogBody {
		p.log.Info("upstream response", zap.String("request_id", reqID), zap.Duration("latency", time.Since(start)), zap.ByteString("body", limitBody(out, p.cfg.Log.MaxBodySize)))
	}
	_, _ = c.Writer.Write(out)
}

func (p *ChatProxy) handleStream(c *gin.Context, body io.Reader, reqID string, start time.Time) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	flusher, _ := c.Writer.(http.Flusher)
	tracker := transform.NewStreamTracker()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	chunks := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			_, _ = c.Writer.Write(append(line, '\n'))
			if flusher != nil {
				flusher.Flush()
			}
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		outData, err := transform.NormalizeSSEData(data, p.transform, tracker)
		if err != nil {
			outData = data
		}
		_, _ = c.Writer.Write([]byte("data: "))
		_, _ = c.Writer.Write(outData)
		_, _ = c.Writer.Write([]byte("\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		chunks++
	}
	if err := scanner.Err(); err != nil {
		p.log.Error("stream read failed", zap.String("request_id", reqID), zap.Error(err))
	}
	p.log.Info("stream completed", zap.String("request_id", reqID), zap.Int("chunks", chunks), zap.Duration("latency", time.Since(start)))
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		lk := strings.ToLower(k)
		if lk == "host" || lk == "content-length" {
			continue
		}
		dst.Del(k)
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for k, vs := range src {
		lk := strings.ToLower(k)
		if lk == "content-length" || lk == "content-encoding" || lk == "transfer-encoding" {
			continue
		}
		dst.Del(k)
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func limitBody(b []byte, max int64) []byte {
	if max <= 0 || int64(len(b)) <= max {
		return b
	}
	return append(b[:max], []byte("...<truncated>")...)
}
