package config

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	DefaultConfigPath = "config.yaml"
	ConfigPathEnv     = "GATEWAY_CONFIG_PATH"
)

type Config struct {
	Server    ServerConfig     `mapstructure:"server"`
	Metrics   MetricsConfig    `mapstructure:"metrics"`
	Redis     RedisConfig      `mapstructure:"redis"`
	Auth      AuthConfig       `mapstructure:"auth"`
	Cache     CacheConfig      `mapstructure:"cache"`
	Upstream  UpstreamConfig   `mapstructure:"upstream"`
	Providers []ProviderConfig `mapstructure:"providers"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type AuthConfig struct {
	ValidTokens    []string `mapstructure:"valid_tokens"`
	ValidTokensEnv string   `mapstructure:"valid_tokens_env"`
	RateLimitQPS   float64  `mapstructure:"rate_limit_qps"`
	RateLimitBurst int      `mapstructure:"rate_limit_burst"`
}

type ServerConfig struct {
	Host              string   `mapstructure:"host"`
	Port              int      `mapstructure:"port"`
	ReadTimeout       string   `mapstructure:"read_timeout"`
	ReadHeaderTimeout string   `mapstructure:"read_header_timeout"`
	WriteTimeout      string   `mapstructure:"write_timeout"`
	IdleTimeout       string   `mapstructure:"idle_timeout"`
	ShutdownTimeout   string   `mapstructure:"shutdown_timeout"`
	TrustedProxyCIDRs []string `mapstructure:"trusted_proxy_cidrs"`
}

type MetricsConfig struct {
	Path              string   `mapstructure:"path"`
	BearerTokenEnv    string   `mapstructure:"bearer_token_env"`
	BearerToken       string   `mapstructure:"bearer_token"`
	AllowedCIDRs      []string `mapstructure:"allowed_cidrs"`
	RateLimitRPS      float64  `mapstructure:"rate_limit_rps"`
	RateLimitBurst    int      `mapstructure:"rate_limit_burst"`
	EnableOpenMetrics bool     `mapstructure:"enable_openmetrics"`
}

type CacheConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	TTL             string `mapstructure:"ttl"`
	MaxPayloadBytes int    `mapstructure:"max_payload_bytes"`
	CoalesceEnabled bool   `mapstructure:"coalesce_enabled"`
}

type UpstreamConfig struct {
	RetryableStatusCodes    []int   `mapstructure:"retryable_status_codes"`
	RetryBackoff            string  `mapstructure:"retry_backoff"`
	DefaultMaxRetries       int     `mapstructure:"default_max_retries"`
	HealthCheckInterval     string  `mapstructure:"health_check_interval"`
	HealthCheckTimeout      string  `mapstructure:"health_check_timeout"`
	BreakerInterval         string  `mapstructure:"breaker_interval"`
	BreakerTimeout          string  `mapstructure:"breaker_timeout"`
	BreakerFailureRatio     float64 `mapstructure:"breaker_failure_ratio"`
	BreakerMinimumRequests  uint32  `mapstructure:"breaker_minimum_requests"`
	BreakerHalfOpenRequests uint32  `mapstructure:"breaker_half_open_requests"`
}

type ProviderConfig struct {
	Name            string   `mapstructure:"name"`
	BaseURL         string   `mapstructure:"base_url"`
	APIKeyEnv       string   `mapstructure:"api_key_env"`
	APIKey          string   `mapstructure:"api_key"`
	Models          []string `mapstructure:"models"`
	Priority        int      `mapstructure:"priority"`
	MaxRetries      int      `mapstructure:"max_retries"`
	HealthCheckPath string   `mapstructure:"health_check_path"`
}

var GlobalConfig *Config

func ResolveConfigPath(explicitPath string) string {
	if path := strings.TrimSpace(explicitPath); path != "" {
		return path
	}
	if path := strings.TrimSpace(os.Getenv(ConfigPathEnv)); path != "" {
		return path
	}
	return DefaultConfigPath
}

func LoadConfig(path string) error {
	resolvedPath := ResolveConfigPath(path)

	viper.SetConfigFile(resolvedPath)
	viper.SetConfigType("yaml")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	setDefaults()

	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("read config file %q: %w", resolvedPath, err)
	}

	cfg := &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}

	if err := resolveAuthTokens(cfg); err != nil {
		return fmt.Errorf("resolve auth tokens: %w", err)
	}
	if err := resolveProviderAPIKeys(cfg); err != nil {
		return fmt.Errorf("resolve provider api keys: %w", err)
	}
	if err := resolveMetricsConfig(cfg); err != nil {
		return fmt.Errorf("resolve metrics config: %w", err)
	}

	normalizeConfig(cfg)
	applyCacheDefaults(cfg)
	applyUpstreamDefaults(cfg)

	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	GlobalConfig = cfg

	log.Printf(
		"Configuration loaded successfully. path=%s providers=%d provider_names=%s cache_enabled=%t cache_ttl=%s cache_max_payload_bytes=%d",
		resolvedPath,
		len(cfg.Providers),
		strings.Join(providerNames(cfg.Providers), ","),
		cfg.Cache.Enabled,
		cfg.Cache.TTL,
		cfg.Cache.MaxPayloadBytes,
	)

	return nil
}

func setDefaults() {
	viper.SetDefault("server.host", "")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.read_timeout", "300s")
	viper.SetDefault("server.read_header_timeout", "10s")
	viper.SetDefault("server.write_timeout", "300s")
	viper.SetDefault("server.idle_timeout", "120s")
	viper.SetDefault("server.shutdown_timeout", "10s")

	viper.SetDefault("metrics.path", "/metrics")
	viper.SetDefault("metrics.rate_limit_rps", 5)
	viper.SetDefault("metrics.rate_limit_burst", 10)

	viper.SetDefault("auth.rate_limit_qps", 10)
	viper.SetDefault("auth.rate_limit_burst", 20)

	viper.SetDefault("redis.addr", "redis:6379")
	viper.SetDefault("redis.db", 0)

	viper.SetDefault("cache.enabled", true)
	viper.SetDefault("cache.ttl", "24h")
	viper.SetDefault("cache.max_payload_bytes", 1<<20)
	viper.SetDefault("cache.coalesce_enabled", true)

	viper.SetDefault("upstream.retryable_status_codes", []int{429, 500, 502, 503, 504})
	viper.SetDefault("upstream.retry_backoff", "200ms")
	viper.SetDefault("upstream.default_max_retries", 1)
	viper.SetDefault("upstream.health_check_interval", "15s")
	viper.SetDefault("upstream.health_check_timeout", "2s")
	viper.SetDefault("upstream.breaker_interval", "10s")
	viper.SetDefault("upstream.breaker_timeout", "15s")
	viper.SetDefault("upstream.breaker_failure_ratio", 0.5)
	viper.SetDefault("upstream.breaker_minimum_requests", 5)
	viper.SetDefault("upstream.breaker_half_open_requests", 3)
}

func normalizeConfig(cfg *Config) {
	cfg.Server.Host = strings.TrimSpace(cfg.Server.Host)
	cfg.Server.ReadTimeout = strings.TrimSpace(cfg.Server.ReadTimeout)
	cfg.Server.ReadHeaderTimeout = strings.TrimSpace(cfg.Server.ReadHeaderTimeout)
	cfg.Server.WriteTimeout = strings.TrimSpace(cfg.Server.WriteTimeout)
	cfg.Server.IdleTimeout = strings.TrimSpace(cfg.Server.IdleTimeout)
	cfg.Server.ShutdownTimeout = strings.TrimSpace(cfg.Server.ShutdownTimeout)

	cfg.Metrics.Path = strings.TrimSpace(cfg.Metrics.Path)
	cfg.Metrics.BearerTokenEnv = strings.TrimSpace(cfg.Metrics.BearerTokenEnv)
	cfg.Metrics.BearerToken = strings.TrimSpace(cfg.Metrics.BearerToken)

	cfg.Redis.Addr = strings.TrimSpace(cfg.Redis.Addr)
	cfg.Redis.Password = strings.TrimSpace(cfg.Redis.Password)

	cfg.Auth.ValidTokensEnv = strings.TrimSpace(cfg.Auth.ValidTokensEnv)
	cfg.Auth.ValidTokens = uniqueNonEmpty(cfg.Auth.ValidTokens)

	cfg.Cache.TTL = strings.TrimSpace(cfg.Cache.TTL)

	for i := range cfg.Server.TrustedProxyCIDRs {
		cfg.Server.TrustedProxyCIDRs[i] = strings.TrimSpace(cfg.Server.TrustedProxyCIDRs[i])
	}
	cfg.Server.TrustedProxyCIDRs = uniqueNonEmpty(cfg.Server.TrustedProxyCIDRs)

	for i := range cfg.Metrics.AllowedCIDRs {
		cfg.Metrics.AllowedCIDRs[i] = strings.TrimSpace(cfg.Metrics.AllowedCIDRs[i])
	}
	cfg.Metrics.AllowedCIDRs = uniqueNonEmpty(cfg.Metrics.AllowedCIDRs)

	cfg.Upstream.RetryBackoff = strings.TrimSpace(cfg.Upstream.RetryBackoff)
	cfg.Upstream.HealthCheckInterval = strings.TrimSpace(cfg.Upstream.HealthCheckInterval)
	cfg.Upstream.HealthCheckTimeout = strings.TrimSpace(cfg.Upstream.HealthCheckTimeout)
	cfg.Upstream.BreakerInterval = strings.TrimSpace(cfg.Upstream.BreakerInterval)
	cfg.Upstream.BreakerTimeout = strings.TrimSpace(cfg.Upstream.BreakerTimeout)

	for i := range cfg.Providers {
		provider := &cfg.Providers[i]
		provider.Name = strings.TrimSpace(provider.Name)
		provider.BaseURL = strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
		provider.APIKeyEnv = strings.TrimSpace(provider.APIKeyEnv)
		provider.APIKey = strings.TrimSpace(provider.APIKey)
		provider.Models = uniqueNonEmpty(provider.Models)
		provider.HealthCheckPath = strings.TrimSpace(provider.HealthCheckPath)
	}
}

func resolveAuthTokens(cfg *Config) error {
	envName := strings.TrimSpace(cfg.Auth.ValidTokensEnv)
	if envName == "" {
		cfg.Auth.ValidTokens = uniqueNonEmpty(cfg.Auth.ValidTokens)
		return nil
	}

	rawValue, ok := os.LookupEnv(envName)
	if !ok || strings.TrimSpace(rawValue) == "" {
		return fmt.Errorf("auth token environment variable %s is not set", envName)
	}

	cfg.Auth.ValidTokens = mergeUnique(cfg.Auth.ValidTokens, splitEnvList(rawValue))
	return nil
}

func resolveProviderAPIKeys(cfg *Config) error {
	for i := range cfg.Providers {
		provider := &cfg.Providers[i]

		if strings.TrimSpace(provider.APIKey) != "" {
			return fmt.Errorf(
				"provider %q still uses inline api_key in config file; remove it and use api_key_env instead",
				provider.Name,
			)
		}

		envName := strings.TrimSpace(provider.APIKeyEnv)
		if envName == "" {
			continue
		}

		envValue, ok := os.LookupEnv(envName)
		if !ok || strings.TrimSpace(envValue) == "" {
			return fmt.Errorf(
				"provider %q requires environment variable %s, but it is not set",
				provider.Name,
				envName,
			)
		}

		provider.APIKey = strings.TrimSpace(envValue)
	}

	return nil
}

func resolveMetricsConfig(cfg *Config) error {
	if strings.TrimSpace(cfg.Metrics.Path) == "" {
		cfg.Metrics.Path = "/metrics"
	}

	if strings.TrimSpace(cfg.Metrics.BearerToken) != "" {
		return fmt.Errorf("metrics.bearer_token must not be set in config file; use metrics.bearer_token_env instead")
	}

	envName := strings.TrimSpace(cfg.Metrics.BearerTokenEnv)
	if envName != "" {
		envValue, ok := os.LookupEnv(envName)
		if ok && strings.TrimSpace(envValue) != "" {
			cfg.Metrics.BearerToken = strings.TrimSpace(envValue)
		}
	}

	if cfg.Metrics.RateLimitRPS <= 0 {
		cfg.Metrics.RateLimitRPS = 5
	}
	if cfg.Metrics.RateLimitBurst <= 0 {
		cfg.Metrics.RateLimitBurst = 10
	}

	return nil
}

func applyCacheDefaults(cfg *Config) {
	if strings.TrimSpace(cfg.Cache.TTL) == "" {
		cfg.Cache.TTL = "24h"
	}
	if cfg.Cache.MaxPayloadBytes <= 0 {
		cfg.Cache.MaxPayloadBytes = 1 << 20
	}
}

func applyUpstreamDefaults(cfg *Config) {
	if cfg.Upstream.DefaultMaxRetries < 0 {
		cfg.Upstream.DefaultMaxRetries = 0
	}
	if len(cfg.Upstream.RetryableStatusCodes) == 0 {
		cfg.Upstream.RetryableStatusCodes = []int{httpStatusTooManyRequests, 500, 502, 503, 504}
	}
	if strings.TrimSpace(cfg.Upstream.RetryBackoff) == "" {
		cfg.Upstream.RetryBackoff = "200ms"
	}
	if strings.TrimSpace(cfg.Upstream.HealthCheckInterval) == "" {
		cfg.Upstream.HealthCheckInterval = "15s"
	}
	if strings.TrimSpace(cfg.Upstream.HealthCheckTimeout) == "" {
		cfg.Upstream.HealthCheckTimeout = "2s"
	}
	if strings.TrimSpace(cfg.Upstream.BreakerInterval) == "" {
		cfg.Upstream.BreakerInterval = "10s"
	}
	if strings.TrimSpace(cfg.Upstream.BreakerTimeout) == "" {
		cfg.Upstream.BreakerTimeout = "15s"
	}
	if cfg.Upstream.BreakerFailureRatio <= 0 || cfg.Upstream.BreakerFailureRatio > 1 {
		cfg.Upstream.BreakerFailureRatio = 0.5
	}
	if cfg.Upstream.BreakerMinimumRequests == 0 {
		cfg.Upstream.BreakerMinimumRequests = 5
	}
	if cfg.Upstream.BreakerHalfOpenRequests == 0 {
		cfg.Upstream.BreakerHalfOpenRequests = 3
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].Priority <= 0 {
			cfg.Providers[i].Priority = 100
		}
		if cfg.Providers[i].MaxRetries < 0 {
			cfg.Providers[i].MaxRetries = 0
		}
	}
}

func validateConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}
	if err := validatePositiveDuration("server.read_timeout", cfg.Server.ReadTimeout); err != nil {
		return err
	}
	if err := validatePositiveDuration("server.read_header_timeout", cfg.Server.ReadHeaderTimeout); err != nil {
		return err
	}
	if err := validatePositiveDuration("server.write_timeout", cfg.Server.WriteTimeout); err != nil {
		return err
	}
	if err := validatePositiveDuration("server.idle_timeout", cfg.Server.IdleTimeout); err != nil {
		return err
	}
	if err := validatePositiveDuration("server.shutdown_timeout", cfg.Server.ShutdownTimeout); err != nil {
		return err
	}

	if cfg.Redis.DB < 0 {
		return fmt.Errorf("redis.db must be >= 0")
	}
	if strings.TrimSpace(cfg.Redis.Addr) == "" {
		return fmt.Errorf("redis.addr cannot be empty")
	}

	if cfg.Auth.RateLimitQPS <= 0 {
		return fmt.Errorf("auth.rate_limit_qps must be > 0")
	}
	if cfg.Auth.RateLimitBurst <= 0 {
		return fmt.Errorf("auth.rate_limit_burst must be > 0")
	}
	if len(cfg.Auth.ValidTokens) == 0 {
		return fmt.Errorf("at least one gateway token must be configured via auth.valid_tokens or auth.valid_tokens_env")
	}

	if err := validatePositiveDuration("cache.ttl", cfg.Cache.TTL); err != nil {
		return err
	}
	if cfg.Cache.MaxPayloadBytes <= 0 {
		return fmt.Errorf("cache.max_payload_bytes must be > 0")
	}

	for _, durationValue := range []struct {
		name  string
		value string
	}{
		{name: "upstream.retry_backoff", value: cfg.Upstream.RetryBackoff},
		{name: "upstream.health_check_interval", value: cfg.Upstream.HealthCheckInterval},
		{name: "upstream.health_check_timeout", value: cfg.Upstream.HealthCheckTimeout},
		{name: "upstream.breaker_interval", value: cfg.Upstream.BreakerInterval},
		{name: "upstream.breaker_timeout", value: cfg.Upstream.BreakerTimeout},
	} {
		if err := validatePositiveDuration(durationValue.name, durationValue.value); err != nil {
			return err
		}
	}

	if !strings.HasPrefix(cfg.Metrics.Path, "/") {
		return fmt.Errorf("metrics.path must start with /")
	}
	if cfg.Metrics.RateLimitRPS <= 0 {
		return fmt.Errorf("metrics.rate_limit_rps must be > 0")
	}
	if cfg.Metrics.RateLimitBurst <= 0 {
		return fmt.Errorf("metrics.rate_limit_burst must be > 0")
	}

	for _, cidr := range cfg.Server.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid trusted proxy cidr %q: %w", cidr, err)
		}
	}
	for _, cidr := range cfg.Metrics.AllowedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid metrics allowed cidr %q: %w", cidr, err)
		}
	}

	if len(cfg.Providers) == 0 {
		return fmt.Errorf("at least one provider must be configured")
	}

	seenProviders := make(map[string]struct{}, len(cfg.Providers))
	for _, provider := range cfg.Providers {
		if provider.Name == "" {
			return fmt.Errorf("provider name cannot be empty")
		}
		if _, exists := seenProviders[provider.Name]; exists {
			return fmt.Errorf("duplicate provider name: %s", provider.Name)
		}
		seenProviders[provider.Name] = struct{}{}

		if err := validateProviderBaseURL(provider.Name, provider.BaseURL); err != nil {
			return err
		}
		if len(provider.Models) == 0 {
			return fmt.Errorf("provider %q must configure at least one model", provider.Name)
		}
		if provider.Priority <= 0 {
			return fmt.Errorf("provider %q priority must be greater than 0", provider.Name)
		}
		if provider.MaxRetries < 0 {
			return fmt.Errorf("provider %q max_retries must be greater than or equal to 0", provider.Name)
		}
	}

	return nil
}

const httpStatusTooManyRequests = 429

func validatePositiveDuration(fieldName, raw string) error {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", fieldName, err)
	}
	if d <= 0 {
		return fmt.Errorf("%s must be > 0", fieldName)
	}
	return nil
}

func validateProviderBaseURL(providerName, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("provider %q base_url is invalid: %w", providerName, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("provider %q base_url must include scheme and host", providerName)
	}

	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if isPrivateOrLoopbackHost(parsed.Hostname()) {
			return nil
		}
		return fmt.Errorf("provider %q base_url must use https unless it points to a private or loopback host", providerName)
	default:
		return fmt.Errorf("provider %q base_url must use http or https", providerName)
	}
}

func isPrivateOrLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

func providerNames(providers []ProviderConfig) []string {
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		if provider.Name != "" {
			names = append(names, provider.Name)
		}
	}
	sort.Strings(names)
	return names
}

func uniqueNonEmpty(items []string) []string {
	if len(items) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		normalized := strings.TrimSpace(item)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}

	return out
}

func mergeUnique(base []string, extra []string) []string {
	return uniqueNonEmpty(append(base, extra...))
}

func splitEnvList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '\n', '\r', ';':
			return true
		default:
			return false
		}
	})
	return uniqueNonEmpty(fields)
}
