package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/archinfra/sbgw/internal/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type TokenStore struct {
	enabled bool
	header  string
	tokens  []string
}

func NewTokenStore(cfg config.AuthConfig) *TokenStore {
	return &TokenStore{enabled: cfg.Enabled, header: cfg.Header, tokens: cfg.Tokens}
}

func (s *TokenStore) Enabled() bool { return s.enabled }

func (s *TokenStore) Valid(raw string) bool {
	if !s.enabled {
		return true
	}
	token := normalizeToken(raw)
	if token == "" {
		return false
	}
	for _, allowed := range s.tokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(strings.TrimSpace(allowed))) == 1 {
			return true
		}
	}
	return false
}

func Middleware(store *TokenStore, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !store.Enabled() {
			c.Next()
			return
		}
		raw := c.GetHeader(store.header)
		if !store.Valid(raw) {
			log.Warn("unauthorized request", zap.String("path", c.Request.URL.Path), zap.String("client_ip", c.ClientIP()))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "unauthorized", "type": "invalid_api_key"}})
			return
		}
		c.Next()
	}
}

func normalizeToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(raw), "bearer ") {
		return strings.TrimSpace(raw[7:])
	}
	return raw
}
