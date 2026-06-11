package proxy

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/archinfra/sbgw/internal/auth"
	"github.com/archinfra/sbgw/internal/config"
	"github.com/archinfra/sbgw/internal/transform"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ChatProxy struct {
	cfg           *config.Config
	log           *zap.Logger
	client        *http.Client
	pool          *upstreamPool
	auth          *auth.TokenStore
	transform     transform.Options
	routesByPath  map[string]config.RouteConfig
	routesByModel map[string]config.RouteConfig
}

func NewChatProxy(cfg *config.Config, log *zap.Logger, tokenStore *auth.TokenStore) (*ChatProxy, error) {
	pool, err := newUpstreamPool(cfg.Upstream)
	if err != nil {
		return nil, err
	}
	routesByPath := map[string]config.RouteConfig{}
	routesByModel := map[string]config.RouteConfig{}
	for _, r := range cfg.Upstream.Routes {
		routesByPath[strings.Trim(r.Path, "/")] = r
		if r.Model != "" {
			routesByModel[r.Model] = r
		}
	}
	return &ChatProxy{
		cfg:           cfg,
		log:           log,
		client:        &http.Client{},
		pool:          pool,
		auth:          tokenStore,
		routesByPath:  routesByPath,
		routesByModel: routesByModel,
		transform: transform.Options{
			Enabled:               cfg.Transform.Enabled,
			InjectThinkTag:        cfg.Transform.InjectThinkTag,
			StripReasoningFields:  cfg.Transform.StripReasoningFields,
			ParseThinkFromContent: cfg.Transform.ParseThinkFromContent,
			ReasoningFields:       cfg.Transform.ReasoningFields,
		},
	}, nil
}

func (p *ChatProxy) HandleModels(c *gin.Context) {
	routeParam := strings.Trim(c.Param("route"), "/")
	if routeParam != "" {
		route, ok, routeErr := p.resolveRoute(routeParam, "")
		if routeErr != "" || !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": routeErr}})
			return
		}
		id := route.Model
		if id == "" {
			id = route.Name
		}
		c.JSON(http.StatusOK, gin.H{"object": "list", "data": []gin.H{{"id": id, "object": "model", "owned_by": "sbgw", "route": route.Name}}})
		return
	}

	ids := p.pool.modelIDs(p.cfg.Upstream.ModelMap, p.cfg.Upstream.Routes)
	data := make([]gin.H, 0, len(ids))
	for _, id := range ids {
		data = append(data, gin.H{"id": id, "object": "model", "owned_by": "sbgw"})
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
}

func (p *ChatProxy) HandleUsage(c *gin.Context) {
	clientID := c.GetString(auth.ContextClientID)
	if !p.auth.Enabled() || clientID == "" || clientID == "anonymous" {
		c.JSON(http.StatusOK, gin.H{"object": "list", "data": p.auth.Snapshots()})
		return
	}
	c.JSON(http.StatusOK, p.auth.Snapshot(clientID))
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

	body, reqInfo, _ := transform.NormalizeRequest(body, transform.RequestOptions{
		Enabled:               p.cfg.Transform.Enabled,
		ReorderSystemMessages: p.cfg.Transform.ReorderSystemMessages,
	})

	routeParam := strings.Trim(c.Param("route"), "/")
	route, routeOK, routeErr := p.resolveRoute(routeParam, reqInfo.Model)
	if routeErr != "" {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": routeErr}})
		return
	}

	logicalModel := reqInfo.Model
	if routeOK && route.Model != "" {
		logicalModel = route.Model
	}
	upstreamModel := ""
	if routeOK && route.UpstreamModel != "" {
		upstreamModel = route.UpstreamModel
	} else {
		upstreamModel = p.cfg.Upstream.ModelMap[logicalModel]
	}

	if routeOK && len(route.RequestPatches) > 0 {
		patches := make([]transform.RequestPatch, 0, len(route.RequestPatches))
		for _, patch := range route.RequestPatches {
			patches = append(patches, transform.RequestPatch{Op: patch.Op, Path: patch.Path, Value: patch.Value})
		}
		patched, result, err := transform.ApplyRequestPatches(body, patches)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "apply route request patches failed", "detail": err.Error()}})
			return
		}
		body = patched
		if result.Applied > 0 {
			p.log.Info("route request patches applied", zap.String("request_id", reqID), zap.String("route", route.Name), zap.String("route_path", route.Path), zap.Int("patches", result.Applied))
		}
	}

	if upstreamModel != "" {
		if rewritten, changed, err := transform.RewriteModel(body, upstreamModel); err == nil && changed {
			body = rewritten
		}
	} else if routeOK && route.Model != "" && reqInfo.Model != route.Model {
		if rewritten, changed, err := transform.RewriteModel(body, route.Model); err == nil && changed {
			body = rewritten
		}
	}

	epNames := []string(nil)
	if routeOK {
		epNames = route.Endpoints
	}
	ep := p.pool.selectEndpoint(logicalModel, epNames)
	if ep == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "no upstream endpoint supports requested model", "model": logicalModel, "route": route.Name}})
		return
	}
	inflight := ep.begin()
	defer ep.end()

	if p.cfg.Log.LogBody {
		p.log.Info("gateway request body", zap.String("request_id", reqID), zap.String("client", c.GetString(auth.ContextClientName)), zap.String("route", route.Name), zap.String("model", logicalModel), zap.String("upstream_model", upstreamModel), zap.ByteString("body", limitBody(body, p.cfg.Log.MaxBodySize)))
	}
	if p.cfg.Log.LogHeaders {
		p.log.Info("gateway request headers", zap.String("request_id", reqID), zap.Any("headers", redactHeaders(c.Request.Header, p.cfg.Log.RedactHeaders)))
	}
	if reqInfo.SystemMessagesReordered {
		p.log.Info("system messages reordered", zap.String("request_id", reqID), zap.String("model", logicalModel), zap.String("route", route.Name))
	}

	upstreamPath := "/v1/chat/completions"
	if routeOK && route.UpstreamPath != "" {
		upstreamPath = route.UpstreamPath
	}
	target := *ep.url
	target.Path = joinURLPath(target.Path, upstreamPath)

	ctx := c.Request.Context()
	cancel := func() {}
	if ep.cfg.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, ep.cfg.Timeout)
	}
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, c.Request.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	copyHeaders(req.Header, c.Request.Header)
	if ep.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+ep.cfg.APIKey)
	} else if ep.cfg.ForwardClientAuthorization == nil || !*ep.cfg.ForwardClientAuthorization {
		// Local SK tokens are for gateway access. Do not leak them to upstream by default.
		req.Header.Del("Authorization")
	}
	req.Header.Set("X-Request-ID", reqID)

	start := time.Now()
	p.log.Info("upstream selected",
		zap.String("request_id", reqID),
		zap.String("strategy", p.cfg.Upstream.Strategy),
		zap.String("route", route.Name),
		zap.String("route_path", route.Path),
		zap.String("upstream", ep.cfg.Name),
		zap.String("upstream_base_url", ep.cfg.BaseURL),
		zap.String("upstream_path", upstreamPath),
		zap.String("model", logicalModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", reqInfo.Stream),
		zap.Int64("inflight", inflight),
	)

	resp, err := p.client.Do(req)
	if err != nil {
		p.log.Error("upstream request failed", zap.String("request_id", reqID), zap.String("upstream", ep.cfg.Name), zap.String("route", route.Name), zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "upstream request failed", "detail": err.Error()}})
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(c.Writer.Header(), resp.Header)
	c.Writer.Header().Set("X-Request-ID", reqID)
	c.Writer.Header().Set("X-SBGW-Upstream", ep.cfg.Name)
	if routeOK {
		c.Writer.Header().Set("X-SBGW-Route", route.Name)
	}
	c.Status(resp.StatusCode)

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		p.handleStream(c, resp.Body, reqID, start, ep, logicalModel)
		return
	}
	p.handleNonStream(c, resp.Body, reqID, start, ep, logicalModel)
}

func (p *ChatProxy) resolveRoute(routeParam string, model string) (config.RouteConfig, bool, string) {
	if routeParam != "" {
		if r, ok := p.routesByPath[routeParam]; ok {
			return r, true, ""
		}
		return config.RouteConfig{}, false, "unknown gateway model route: " + routeParam
	}
	if model != "" {
		if r, ok := p.routesByModel[model]; ok {
			return r, true, ""
		}
	}
	return config.RouteConfig{}, false, ""
}

func joinURLPath(base string, suffix string) string {
	base = strings.TrimRight(base, "/")
	if suffix == "" {
		return base
	}
	if !strings.HasPrefix(suffix, "/") {
		suffix = "/" + suffix
	}
	return base + suffix
}

func (p *ChatProxy) handleNonStream(c *gin.Context, body io.Reader, reqID string, start time.Time, ep *upstreamEndpoint, model string) {
	respBody, err := io.ReadAll(body)
	if err != nil {
		p.log.Error("read upstream response failed", zap.String("request_id", reqID), zap.String("upstream", ep.cfg.Name), zap.Error(err))
		return
	}
	out, err := transform.NormalizeNonStream(respBody, p.transform)
	if err != nil {
		out = respBody
	}
	tokens := transform.ExtractTotalTokens(out)
	p.recordUsage(c, tokens, reqID, model)
	if p.cfg.Log.LogBody {
		p.log.Info("upstream response body", zap.String("request_id", reqID), zap.String("upstream", ep.cfg.Name), zap.Duration("latency", time.Since(start)), zap.Int64("total_tokens", tokens), zap.ByteString("body", limitBody(out, p.cfg.Log.MaxBodySize)))
	}
	p.log.Info("completion finished", zap.String("request_id", reqID), zap.String("upstream", ep.cfg.Name), zap.String("model", model), zap.Int("status", c.Writer.Status()), zap.Int64("total_tokens", tokens), zap.Duration("latency", time.Since(start)), zap.Int64("inflight", ep.currentInflight()))
	_, _ = c.Writer.Write(out)
}

func (p *ChatProxy) handleStream(c *gin.Context, body io.Reader, reqID string, start time.Time, ep *upstreamEndpoint, model string) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	flusher, _ := c.Writer.(http.Flusher)
	tracker := transform.NewStreamTracker()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	chunks := 0
	var totalTokens int64
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
		if n := transform.ExtractTotalTokens(outData); n > 0 {
			totalTokens = n
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
		p.log.Error("stream read failed", zap.String("request_id", reqID), zap.String("upstream", ep.cfg.Name), zap.Error(err))
	}
	p.recordUsage(c, totalTokens, reqID, model)
	p.log.Info("stream completed", zap.String("request_id", reqID), zap.String("upstream", ep.cfg.Name), zap.String("model", model), zap.Int("chunks", chunks), zap.Int64("total_tokens", totalTokens), zap.Duration("latency", time.Since(start)), zap.Int64("inflight", ep.currentInflight()))
}

func (p *ChatProxy) recordUsage(c *gin.Context, tokens int64, reqID string, model string) {
	if tokens <= 0 || p.auth == nil {
		return
	}
	clientID := c.GetString(auth.ContextClientID)
	if clientID == "" {
		return
	}
	snap := p.auth.RecordUsage(clientID, tokens)
	if !snap.Unlimited {
		c.Header("X-SBGW-Quota-Used", int64ToString(snap.UsedTokens))
		c.Header("X-SBGW-Quota-Remaining", int64ToString(snap.RemainingTokens))
	}
	p.log.Info("client token usage recorded", zap.String("request_id", reqID), zap.String("client", snap.Name), zap.String("client_id", snap.ID), zap.String("model", model), zap.Int64("tokens", tokens), zap.Int64("used_tokens", snap.UsedTokens), zap.Int64("remaining_tokens", snap.RemainingTokens), zap.Bool("unlimited", snap.Unlimited))
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		lk := strings.ToLower(k)
		if lk == "host" || lk == "content-length" || lk == "connection" || lk == "keep-alive" || lk == "proxy-authenticate" || lk == "proxy-authorization" || lk == "te" || lk == "trailer" || lk == "transfer-encoding" || lk == "upgrade" {
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
		if lk == "content-length" || lk == "content-encoding" || lk == "transfer-encoding" || lk == "connection" {
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

func redactHeaders(h http.Header, redacted []string) map[string][]string {
	deny := map[string]struct{}{}
	for _, k := range redacted {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != "" {
			deny[k] = struct{}{}
		}
	}
	out := map[string][]string{}
	for k, vs := range h {
		if _, ok := deny[strings.ToLower(k)]; ok {
			out[k] = []string{"<redacted>"}
			continue
		}
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func int64ToString(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
