package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/archinfra/sbgw/internal/auth"
	"github.com/archinfra/sbgw/internal/config"
	"github.com/archinfra/sbgw/internal/transform"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func routeAllowsChat(kind string) bool {
	return kind == "" || kind == config.RouteKindChat
}

func routeAllowsAudioTranscription(kind string) bool {
	return kind == config.RouteKindAudioTranscription
}

// HandleAudioTranscriptions proxies OpenAI-compatible ASR requests to upstream
// /v1/audio/transcriptions endpoints. It supports both standard
// /v1/audio/transcriptions and route-prefixed /{route}/v1/audio/transcriptions
// forms. Multipart model fields are safely rewritten without logging binary
// audio content.
func (p *ChatProxy) HandleAudioTranscriptions(c *gin.Context) {
	reqID := c.GetHeader("X-Request-ID")
	if reqID == "" {
		reqID = uuid.NewString()
	}
	c.Set("request_id", reqID)

	routeParam := strings.Trim(c.Param("route"), "/")
	contentType := c.GetHeader("Content-Type")
	isMultipart := strings.HasPrefix(strings.ToLower(contentType), "multipart/form-data")

	var rawBody []byte
	var reqModel string
	var err error
	if isMultipart {
		if err = c.Request.ParseMultipartForm(64 << 20); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "parse multipart audio transcription request failed", "detail": err.Error()}})
			return
		}
		if c.Request.MultipartForm != nil {
			defer c.Request.MultipartForm.RemoveAll()
			reqModel = firstFormValue(c.Request.MultipartForm, "model")
		}
	} else {
		rawBody, err = io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "read request body failed"}})
			return
		}
		reqModel = requestModelFromJSON(rawBody)
	}

	route, routeOK, routeErr := p.resolveRoute(routeParam, reqModel)
	if routeErr != "" {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": routeErr}})
		return
	}
	if routeOK && !routeAllowsAudioTranscription(route.Kind) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "route does not support audio transcriptions", "route": route.Name, "kind": route.Kind}})
		return
	}

	logicalModel := reqModel
	if routeOK && route.Model != "" {
		logicalModel = route.Model
	}
	upstreamModel := ""
	if routeOK && route.UpstreamModel != "" {
		upstreamModel = route.UpstreamModel
	} else {
		upstreamModel = p.cfg.Upstream.ModelMap[logicalModel]
	}
	modelForUpstream := upstreamModel
	if modelForUpstream == "" && routeOK && route.Model != "" && reqModel != route.Model {
		modelForUpstream = route.Model
	}

	body := rawBody
	outboundContentType := contentType
	bodySummary := "json_or_raw"
	if isMultipart {
		body, outboundContentType, bodySummary, err = buildAudioMultipartBody(c.Request.MultipartForm, modelForUpstream, route.RequestPatches)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "rebuild multipart audio transcription request failed", "detail": err.Error()}})
			return
		}
	} else {
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
				p.log.Info("audio request patches applied", zap.String("request_id", reqID), zap.String("route", route.Name), zap.String("route_kind", route.Kind), zap.String("adapter", route.Adapter), zap.Int("patches", result.Applied))
			}
		}
		if modelForUpstream != "" {
			if rewritten, changed, err := transform.RewriteModel(body, modelForUpstream); err == nil && changed {
				body = rewritten
			}
		}
	}

	epNames := []string(nil)
	if routeOK {
		epNames = route.Endpoints
	}
	ep := p.pool.selectEndpoint(logicalModel, epNames)
	if ep == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "no upstream endpoint supports requested audio model", "model": logicalModel, "route": route.Name}})
		return
	}
	inflight := ep.begin()
	defer ep.end()

	upstreamPath := "/v1/audio/transcriptions"
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
	if outboundContentType != "" {
		req.Header.Set("Content-Type", outboundContentType)
	}
	if ep.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+ep.cfg.APIKey)
	} else if ep.cfg.ForwardClientAuthorization == nil || !*ep.cfg.ForwardClientAuthorization {
		req.Header.Del("Authorization")
	}
	req.Header.Set("X-Request-ID", reqID)

	start := time.Now()
	p.log.Info("audio upstream selected",
		zap.String("request_id", reqID),
		zap.String("strategy", p.cfg.Upstream.Strategy),
		zap.String("route", route.Name),
		zap.String("route_path", route.Path),
		zap.String("route_kind", route.Kind),
		zap.String("adapter", route.Adapter),
		zap.String("upstream", ep.cfg.Name),
		zap.String("upstream_base_url", ep.cfg.BaseURL),
		zap.String("upstream_path", upstreamPath),
		zap.String("model", logicalModel),
		zap.String("upstream_model", upstreamModel),
		zap.String("body_summary", bodySummary),
		zap.Int64("inflight", inflight),
	)

	resp, err := p.client.Do(req)
	if err != nil {
		p.log.Error("audio upstream request failed", zap.String("request_id", reqID), zap.String("upstream", ep.cfg.Name), zap.String("route", route.Name), zap.Error(err))
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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		p.log.Error("read audio upstream response failed", zap.String("request_id", reqID), zap.String("upstream", ep.cfg.Name), zap.Error(err))
		return
	}
	tokens := transform.ExtractTotalTokens(respBody)
	p.recordUsage(c, tokens, reqID, logicalModel)
	if p.cfg.Log.LogBody {
		p.log.Info("audio upstream response body", zap.String("request_id", reqID), zap.String("upstream", ep.cfg.Name), zap.Duration("latency", time.Since(start)), zap.Int64("total_tokens", tokens), zap.ByteString("body", limitBody(respBody, p.cfg.Log.MaxBodySize)))
	}
	p.log.Info("audio transcription finished", zap.String("request_id", reqID), zap.String("upstream", ep.cfg.Name), zap.String("model", logicalModel), zap.Int("status", c.Writer.Status()), zap.Int64("total_tokens", tokens), zap.Duration("latency", time.Since(start)), zap.Int64("inflight", ep.currentInflight()))
	_, _ = c.Writer.Write(respBody)
}

func requestModelFromJSON(body []byte) string {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return ""
	}
	model, _ := root["model"].(string)
	return strings.TrimSpace(model)
}

func firstFormValue(form *multipart.Form, key string) string {
	if form == nil || form.Value == nil {
		return ""
	}
	vs := form.Value[key]
	if len(vs) == 0 {
		return ""
	}
	return strings.TrimSpace(vs[0])
}

func buildAudioMultipartBody(form *multipart.Form, modelForUpstream string, patches []config.RequestPatchConfig) ([]byte, string, string, error) {
	if form == nil {
		return nil, "", "", fmt.Errorf("multipart form is empty")
	}
	values := map[string][]string{}
	for k, vs := range form.Value {
		values[k] = append([]string(nil), vs...)
	}
	if modelForUpstream != "" {
		values["model"] = []string{modelForUpstream}
	}
	applied := applyFormPatches(values, patches)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	valueKeys := make([]string, 0, len(values))
	for k := range values {
		valueKeys = append(valueKeys, k)
	}
	sort.Strings(valueKeys)
	for _, k := range valueKeys {
		for _, v := range values[k] {
			if err := writer.WriteField(k, v); err != nil {
				return nil, "", "", err
			}
		}
	}

	fileFields := make([]string, 0, len(form.File))
	for field := range form.File {
		fileFields = append(fileFields, field)
	}
	sort.Strings(fileFields)
	fileCount := 0
	for _, field := range fileFields {
		for _, fh := range form.File[field] {
			file, err := fh.Open()
			if err != nil {
				return nil, "", "", err
			}
			part, err := writer.CreateFormFile(field, fh.Filename)
			if err != nil {
				_ = file.Close()
				return nil, "", "", err
			}
			if _, err = io.Copy(part, file); err != nil {
				_ = file.Close()
				return nil, "", "", err
			}
			_ = file.Close()
			fileCount++
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", "", err
	}

	summary := fmt.Sprintf("multipart fields=%d files=%d patches=%d", len(values), fileCount, applied)
	return buf.Bytes(), writer.FormDataContentType(), summary, nil
}

func applyFormPatches(values map[string][]string, patches []config.RequestPatchConfig) int {
	applied := 0
	for _, patch := range patches {
		key := strings.TrimSpace(strings.Trim(patch.Path, "."))
		if key == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(patch.Op)) {
		case "delete":
			if _, ok := values[key]; ok {
				delete(values, key)
				applied++
			}
		case "", "set":
			value := formPatchValueToString(patch.Value)
			if existing := values[key]; len(existing) == 1 && existing[0] == value {
				continue
			}
			values[key] = []string{value}
			applied++
		}
	}
	return applied
}

func formPatchValueToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprint(t)
	}
}
