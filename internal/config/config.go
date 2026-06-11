package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	Log       LogConfig       `mapstructure:"log"`
	Auth      AuthConfig      `mapstructure:"auth"`
	Upstream  UpstreamConfig  `mapstructure:"upstream"`
	Transform TransformConfig `mapstructure:"transform"`
}

type ServerConfig struct {
	Addr string `mapstructure:"addr"`
	Mode string `mapstructure:"mode"`
}

type LogConfig struct {
	Level         string   `mapstructure:"level"`
	Format        string   `mapstructure:"format"`
	LogBody       bool     `mapstructure:"log_body"`
	LogHeaders    bool     `mapstructure:"log_headers"`
	MaxBodySize   int64    `mapstructure:"max_body_size"`
	RedactHeaders []string `mapstructure:"redact_headers"`
}

type AuthConfig struct {
	Enabled bool              `mapstructure:"enabled"`
	Tokens  []string          `mapstructure:"tokens"`
	Keys    []ClientKeyConfig `mapstructure:"keys"`
	Header  string            `mapstructure:"header"`
}

type ClientKeyConfig struct {
	Name        string `mapstructure:"name"`
	Key         string `mapstructure:"key"`
	QuotaTokens int64  `mapstructure:"quota_tokens"`
	Disabled    bool   `mapstructure:"disabled"`
}

type UpstreamConfig struct {
	BaseURL                    string                   `mapstructure:"base_url"`
	Timeout                    time.Duration            `mapstructure:"timeout"`
	APIKey                     string                   `mapstructure:"api_key"`
	ForwardClientAuthorization bool                     `mapstructure:"forward_client_authorization"`
	Strategy                   string                   `mapstructure:"strategy"`
	ModelMap                   map[string]string        `mapstructure:"model_map"`
	Endpoints                  []UpstreamEndpointConfig `mapstructure:"endpoints"`
	Routes                     []RouteConfig            `mapstructure:"routes"`
}

type UpstreamEndpointConfig struct {
	Name                       string        `mapstructure:"name"`
	BaseURL                    string        `mapstructure:"base_url"`
	Timeout                    time.Duration `mapstructure:"timeout"`
	APIKey                     string        `mapstructure:"api_key"`
	ForwardClientAuthorization *bool         `mapstructure:"forward_client_authorization"`
	Weight                     int           `mapstructure:"weight"`
	Models                     []string      `mapstructure:"models"`
}

// RouteConfig exposes a gateway-local subpath as a logical model variant.
// Example: /qwen36-think/v1/chat/completions can route to upstream model
// qwen3.6-27b-w8a8 and inject framework-specific thinking parameters.
type RouteConfig struct {
	Name           string               `mapstructure:"name"`
	Path           string               `mapstructure:"path"`
	Model          string               `mapstructure:"model"`
	UpstreamModel  string               `mapstructure:"upstream_model"`
	UpstreamPath   string               `mapstructure:"upstream_path"`
	Endpoints      []string             `mapstructure:"endpoints"`
	RequestPatches []RequestPatchConfig `mapstructure:"request_patches"`
}

type RequestPatchConfig struct {
	Op    string `mapstructure:"op"`
	Path  string `mapstructure:"path"`
	Value any    `mapstructure:"value"`
}

type TransformConfig struct {
	Enabled               bool     `mapstructure:"enabled"`
	InjectThinkTag        bool     `mapstructure:"inject_think_tag"`
	StripReasoningFields  bool     `mapstructure:"strip_reasoning_fields"`
	ParseThinkFromContent bool     `mapstructure:"parse_think_from_content"`
	ReorderSystemMessages bool     `mapstructure:"reorder_system_messages"`
	ReasoningFields       []string `mapstructure:"reasoning_fields"`
}

func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("./configs")
	v.AddConfigPath("/etc/sbgw")

	setDefaults(v)
	v.SetEnvPrefix("SBGW")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, err
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	cfg.Auth.Header = strings.TrimSpace(cfg.Auth.Header)
	if cfg.Auth.Header == "" {
		cfg.Auth.Header = "Authorization"
	}
	normalizeUpstreamDefaults(&cfg)
	return &cfg, validate(&cfg)
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.addr", ":12224")
	v.SetDefault("server.mode", "release")
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")
	v.SetDefault("log.log_body", true)
	v.SetDefault("log.log_headers", false)
	v.SetDefault("log.max_body_size", 8192)
	v.SetDefault("log.redact_headers", []string{"authorization", "x-api-key", "api-key"})
	v.SetDefault("auth.enabled", false)
	v.SetDefault("auth.header", "Authorization")
	v.SetDefault("upstream.base_url", "http://127.0.0.1:18489")
	v.SetDefault("upstream.timeout", "10m")
	v.SetDefault("upstream.forward_client_authorization", false)
	v.SetDefault("upstream.strategy", "weighted_round_robin")
	v.SetDefault("transform.enabled", true)
	v.SetDefault("transform.inject_think_tag", true)
	v.SetDefault("transform.strip_reasoning_fields", true)
	v.SetDefault("transform.parse_think_from_content", true)
	v.SetDefault("transform.reorder_system_messages", true)
	v.SetDefault("transform.reasoning_fields", []string{"reasoning_content", "reasoning"})
}

func normalizeUpstreamDefaults(cfg *Config) {
	cfg.Upstream.Strategy = strings.TrimSpace(strings.ToLower(cfg.Upstream.Strategy))
	if cfg.Upstream.Strategy == "" {
		cfg.Upstream.Strategy = "weighted_round_robin"
	}
	if len(cfg.Upstream.Endpoints) == 0 {
		cfg.Upstream.Endpoints = []UpstreamEndpointConfig{{
			Name:                       "default",
			BaseURL:                    cfg.Upstream.BaseURL,
			Timeout:                    cfg.Upstream.Timeout,
			APIKey:                     cfg.Upstream.APIKey,
			ForwardClientAuthorization: boolPtr(cfg.Upstream.ForwardClientAuthorization),
			Weight:                     1,
		}}
	} else {
		for i := range cfg.Upstream.Endpoints {
			ep := &cfg.Upstream.Endpoints[i]
			ep.Name = strings.TrimSpace(ep.Name)
			if ep.Name == "" {
				ep.Name = fmt.Sprintf("upstream-%d", i+1)
			}
			if ep.Timeout == 0 {
				ep.Timeout = cfg.Upstream.Timeout
			}
			if ep.Weight <= 0 {
				ep.Weight = 1
			}
			if ep.ForwardClientAuthorization == nil {
				ep.ForwardClientAuthorization = boolPtr(cfg.Upstream.ForwardClientAuthorization)
			}
		}
	}

	for i := range cfg.Upstream.Routes {
		r := &cfg.Upstream.Routes[i]
		r.Name = strings.TrimSpace(r.Name)
		r.Path = normalizeRoutePath(r.Path)
		if r.Name == "" {
			r.Name = strings.TrimPrefix(r.Path, "/")
		}
		if r.Model == "" {
			r.Model = r.Name
		}
		if r.UpstreamPath == "" {
			r.UpstreamPath = "/v1/chat/completions"
		}
		if !strings.HasPrefix(r.UpstreamPath, "/") {
			r.UpstreamPath = "/" + r.UpstreamPath
		}
		for j := range r.RequestPatches {
			p := &r.RequestPatches[j]
			p.Op = strings.TrimSpace(strings.ToLower(p.Op))
			if p.Op == "" {
				p.Op = "set"
			}
			p.Path = strings.TrimSpace(p.Path)
		}
	}
}

func normalizeRoutePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}
	return "/" + path
}

func boolPtr(v bool) *bool { return &v }

func validate(cfg *Config) error {
	if len(cfg.Upstream.Endpoints) == 0 {
		return fmt.Errorf("at least one upstream endpoint is required")
	}
	endpointNames := map[string]struct{}{}
	for _, ep := range cfg.Upstream.Endpoints {
		if strings.TrimSpace(ep.BaseURL) == "" {
			return fmt.Errorf("upstream endpoint %q base_url is required", ep.Name)
		}
		endpointNames[ep.Name] = struct{}{}
	}
	seenRoutes := map[string]struct{}{}
	for _, r := range cfg.Upstream.Routes {
		if r.Name == "" {
			return fmt.Errorf("upstream route name is required")
		}
		if r.Path == "" {
			return fmt.Errorf("upstream route %q path is required", r.Name)
		}
		if strings.Contains(strings.Trim(r.Path, "/"), "/") {
			return fmt.Errorf("upstream route %q path must be a single subpath segment, got %q", r.Name, r.Path)
		}
		if _, ok := seenRoutes[r.Path]; ok {
			return fmt.Errorf("duplicate upstream route path %q", r.Path)
		}
		seenRoutes[r.Path] = struct{}{}
		for _, epName := range r.Endpoints {
			epName = strings.TrimSpace(epName)
			if epName == "" {
				continue
			}
			if _, ok := endpointNames[epName]; !ok {
				return fmt.Errorf("upstream route %q references unknown endpoint %q", r.Name, epName)
			}
		}
		for _, p := range r.RequestPatches {
			if p.Path == "" {
				return fmt.Errorf("upstream route %q has request patch with empty path", r.Name)
			}
			switch p.Op {
			case "set", "delete":
			default:
				return fmt.Errorf("upstream route %q has unsupported request patch op %q", r.Name, p.Op)
			}
		}
	}
	switch cfg.Upstream.Strategy {
	case "round_robin", "weighted_round_robin", "random", "weighted_random", "least_inflight":
		return nil
	default:
		return fmt.Errorf("unsupported upstream.strategy %q", cfg.Upstream.Strategy)
	}
}
