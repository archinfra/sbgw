package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/archinfra/sbgw/internal/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	ContextClientID   = "sbgw_client_id"
	ContextClientName = "sbgw_client_name"
)

type Client struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	QuotaTokens int64  `json:"quota_tokens"`
}

type UsageSnapshot struct {
	Client
	UsedTokens      int64 `json:"used_tokens"`
	RemainingTokens int64 `json:"remaining_tokens"`
	Unlimited       bool  `json:"unlimited"`
}

type tokenEntry struct {
	raw string
	Client
}

type TokenStore struct {
	enabled bool
	header  string
	entries []tokenEntry
	mu      sync.RWMutex
	used    map[string]int64
}

func NewTokenStore(cfg config.AuthConfig) *TokenStore {
	entries := make([]tokenEntry, 0, len(cfg.Tokens)+len(cfg.Keys))
	seen := map[string]struct{}{}

	add := func(name, raw string, quota int64, disabled bool) {
		raw = normalizeToken(raw)
		if raw == "" || disabled {
			return
		}
		if _, ok := seen[raw]; ok {
			return
		}
		seen[raw] = struct{}{}
		id := stableClientID(raw)
		name = strings.TrimSpace(name)
		if name == "" {
			name = id
		}
		entries = append(entries, tokenEntry{raw: raw, Client: Client{ID: id, Name: name, QuotaTokens: quota}})
	}

	for i, token := range cfg.Tokens {
		add("legacy-token-"+strconv.Itoa(i+1), token, 0, false)
	}
	for _, key := range cfg.Keys {
		add(key.Name, key.Key, key.QuotaTokens, key.Disabled)
	}

	return &TokenStore{enabled: cfg.Enabled, header: cfg.Header, entries: entries, used: map[string]int64{}}
}

func (s *TokenStore) Enabled() bool { return s.enabled }

func (s *TokenStore) Authenticate(raw string) (Client, bool, bool) {
	if !s.enabled {
		return Client{ID: "anonymous", Name: "anonymous"}, true, false
	}
	token := normalizeToken(raw)
	if token == "" {
		return Client{}, false, false
	}
	for _, entry := range s.entries {
		if subtle.ConstantTimeCompare([]byte(token), []byte(entry.raw)) != 1 {
			continue
		}
		if s.exhaustedLockedSafe(entry.Client) {
			return entry.Client, true, true
		}
		return entry.Client, true, false
	}
	return Client{}, false, false
}

func (s *TokenStore) Valid(raw string) bool {
	_, ok, exhausted := s.Authenticate(raw)
	return ok && !exhausted
}

func (s *TokenStore) RecordUsage(clientID string, tokens int64) UsageSnapshot {
	if tokens < 0 {
		tokens = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.used[clientID]; !ok {
		s.used[clientID] = 0
	}
	s.used[clientID] += tokens
	return s.snapshotLocked(clientID)
}

func (s *TokenStore) Snapshot(clientID string) UsageSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked(clientID)
}

func (s *TokenStore) Snapshots() []UsageSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UsageSnapshot, 0, len(s.entries))
	for _, entry := range s.entries {
		out = append(out, s.snapshotForClientLocked(entry.Client))
	}
	return out
}

func Middleware(store *TokenStore, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		client, ok, exhausted := store.Authenticate(c.GetHeader(store.header))
		if !ok {
			log.Warn("unauthorized request", zap.String("path", c.Request.URL.Path), zap.String("client_ip", c.ClientIP()))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "unauthorized", "type": "invalid_api_key"}})
			return
		}
		if exhausted {
			log.Warn("client token quota exceeded", zap.String("path", c.Request.URL.Path), zap.String("client", client.Name), zap.String("client_id", client.ID), zap.String("client_ip", c.ClientIP()))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"message": "token quota exceeded", "type": "quota_exceeded"}})
			return
		}
		c.Set(ContextClientID, client.ID)
		c.Set(ContextClientName, client.Name)
		if client.QuotaTokens > 0 {
			snap := store.Snapshot(client.ID)
			c.Header("X-SBGW-Quota-Used", int64ToString(snap.UsedTokens))
			c.Header("X-SBGW-Quota-Remaining", int64ToString(snap.RemainingTokens))
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

func stableClientID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "key-" + hex.EncodeToString(sum[:])[:12]
}

func (s *TokenStore) exhaustedLockedSafe(client Client) bool {
	if client.QuotaTokens <= 0 {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.used[client.ID] >= client.QuotaTokens
}

func (s *TokenStore) snapshotLocked(clientID string) UsageSnapshot {
	for _, entry := range s.entries {
		if entry.Client.ID == clientID {
			return s.snapshotForClientLocked(entry.Client)
		}
	}
	used := s.used[clientID]
	return UsageSnapshot{Client: Client{ID: clientID, Name: clientID}, UsedTokens: used, Unlimited: true}
}

func (s *TokenStore) snapshotForClientLocked(client Client) UsageSnapshot {
	used := s.used[client.ID]
	snap := UsageSnapshot{Client: client, UsedTokens: used, Unlimited: client.QuotaTokens <= 0}
	if client.QuotaTokens > 0 {
		snap.RemainingTokens = client.QuotaTokens - used
		if snap.RemainingTokens < 0 {
			snap.RemainingTokens = 0
		}
	}
	return snap
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
