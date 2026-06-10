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
	Level       string `mapstructure:"level"`
	Format      string `mapstructure:"format"`
	LogBody     bool   `mapstructure:"log_body"`
	MaxBodySize int64  `mapstructure:"max_body_size"`
}

type AuthConfig struct {
	Enabled bool     `mapstructure:"enabled"`
	Tokens  []string `mapstructure:"tokens"`
	Header  string   `mapstructure:"header"`
}

type UpstreamConfig struct {
	BaseURL                    string        `mapstructure:"base_url"`
	Timeout                    time.Duration `mapstructure:"timeout"`
	APIKey                     string        `mapstructure:"api_key"`
	ForwardClientAuthorization bool          `mapstructure:"forward_client_authorization"`
}

type TransformConfig struct {
	Enabled               bool     `mapstructure:"enabled"`
	InjectThinkTag        bool     `mapstructure:"inject_think_tag"`
	StripReasoningFields  bool     `mapstructure:"strip_reasoning_fields"`
	ParseThinkFromContent bool     `mapstructure:"parse_think_from_content"`
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
	return &cfg, validate(&cfg)
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.addr", ":12224")
	v.SetDefault("server.mode", "release")
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")
	v.SetDefault("log.log_body", true)
	v.SetDefault("log.max_body_size", 8192)
	v.SetDefault("auth.enabled", false)
	v.SetDefault("auth.header", "Authorization")
	v.SetDefault("upstream.base_url", "http://127.0.0.1:18489")
	v.SetDefault("upstream.timeout", "10m")
	v.SetDefault("upstream.forward_client_authorization", false)
	v.SetDefault("transform.enabled", true)
	v.SetDefault("transform.inject_think_tag", true)
	v.SetDefault("transform.strip_reasoning_fields", true)
	v.SetDefault("transform.parse_think_from_content", true)
	v.SetDefault("transform.reasoning_fields", []string{"reasoning_content", "reasoning"})
}

func validate(cfg *Config) error {
	if strings.TrimSpace(cfg.Upstream.BaseURL) == "" {
		return fmt.Errorf("upstream.base_url is required")
	}
	return nil
}
