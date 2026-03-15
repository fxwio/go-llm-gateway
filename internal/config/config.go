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
	Debug     DebugConfig      `mapstructure:"debug"`
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
	ValidTokens    []string            `mapstructure:"valid_tokens"`
	ValidTokensEnv string              `mapstructure:"valid_tokens_env"`
	RateLimitQPS   float64             `mapstructure:"rate_limit_qps"`
	RateLimitBurst int                 `mapstructure:"rate_limit_burst"`
	Tokens         []ClientTokenConfig `mapstructure:"tokens"`
	Admin          AdminConfig         `mapstructure:"admin"`
}

type ClientTokenConfig struct {
	Name            string  `mapstructure:"name"`
	Tenant          string  `mapstructure:"tenant"`
	App             string  `mapstructure:"app"`
	ValueEnv        string  `mapstructure:"value_env"`
	Value           string  `mapstructure:"value"`
	RateLimitQPS    float64 `mapstructure:"rate_limit_qps"`
	RateLimitBurst  int     `mapstructure:"rate_limit_burst"`
	DailyTokenLimit int64   `mapstructure:"daily_token_limit"`
	Disabled        bool    `mapstructure:"disabled"`
}

type AdminConfig struct {
	BearerTokenEnv string   `mapstructure:"bearer_token_env"`
	BearerToken    string   `mapstructure:"bearer_token"`
	AllowedCIDRs   []string `mapstructure:"allowed_cidrs"`
	RateLimitRPS   float64  `mapstructure:"rate_limit_rps"`
	RateLimitBurst int      `mapstructure:"rate_limit_burst"`
}

type DebugConfig struct {
	PprofEnabled    bool   `mapstructure:"pprof_enabled"`
	PprofPathPrefix string `mapstructure:"pprof_path_prefix"`
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
	if err := resolveStructuredTokens(cfg); err != nil {
		return fmt.Errorf("resolve structured tokens: %w", err)
	}
	if err := resolveProviderAPIKeys(cfg); err != nil {
		return fmt.Errorf("resolve provider api keys: %w", err)
	}
	if err := resolveMetricsConfig(cfg); err != nil {
		return fmt.Errorf("resolve metrics config: %w", err)
	}
	if err := resolveAdminConfig(cfg); err != nil {
		return fmt.Errorf("resolve admin config: %w", err)
	}

	normalizeConfig(cfg)
	applyCacheDefaults(cfg)
	applyUpstreamDefaults(cfg)

	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	GlobalConfig = cfg
	log.Printf(
		"Configuration loaded successfully. path=%s providers=%d provider_names=%s cache_enabled=%t cache_ttl=%s cache_max_payload_bytes=%d structured_tokens=%d",
		resolvedPath,
		len(cfg.Providers),
		strings.Join(providerNames(cfg.Providers), ","),
		cfg.Cache.Enabled,
		cfg.Cache.TTL,
		cfg.Cache.MaxPayloadBytes,
		len(cfg.Auth.Tokens),
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
	viper.SetDefault("debug.pprof_enabled", false)
	viper.SetDefault("debug.pprof_path_prefix", "/debug/pprof")
	viper.SetDefault("auth.rate_limit_qps", 10)
	viper.SetDefault("auth.rate_limit_burst", 20)
	viper.SetDefault("auth.admin.rate_limit_rps", 2)
	viper.SetDefault("auth.admin.rate_limit_burst", 4)
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
	cfg.Debug.PprofPathPrefix = strings.TrimSpace(cfg.Debug.PprofPathPrefix)
	cfg.Redis.Addr = strings.TrimSpace(cfg.Redis.Addr)
	cfg.Redis.Password = strings.TrimSpace(cfg.Redis.Password)
	cfg.Auth.ValidTokensEnv = strings.TrimSpace(cfg.Auth.ValidTokensEnv)
	cfg.Auth.ValidTokens = uniqueNonEmpty(cfg.Auth.ValidTokens)
	cfg.Auth.Admin.BearerTokenEnv = strings.TrimSpace(cfg.Auth.Admin.BearerTokenEnv)
	cfg.Auth.Admin.BearerToken = strings.TrimSpace(cfg.Auth.Admin.BearerToken)
	cfg.Cache.TTL = strings.TrimSpace(cfg.Cache.TTL)
	for i := range cfg.Server.TrustedProxyCIDRs {
		cfg.Server.TrustedProxyCIDRs[i] = strings.TrimSpace(cfg.Server.TrustedProxyCIDRs[i])
	}
	cfg.Server.TrustedProxyCIDRs = uniqueNonEmpty(cfg.Server.TrustedProxyCIDRs)
	for i := range cfg.Metrics.AllowedCIDRs {
		cfg.Metrics.AllowedCIDRs[i] = strings.TrimSpace(cfg.Metrics.AllowedCIDRs[i])
	}
	cfg.Metrics.AllowedCIDRs = uniqueNonEmpty(cfg.Metrics.AllowedCIDRs)
	for i := range cfg.Auth.Admin.AllowedCIDRs {
		cfg.Auth.Admin.AllowedCIDRs[i] = strings.TrimSpace(cfg.Auth.Admin.AllowedCIDRs[i])
	}
	cfg.Auth.Admin.AllowedCIDRs = uniqueNonEmpty(cfg.Auth.Admin.AllowedCIDRs)
	cfg.Upstream.RetryBackoff = strings.TrimSpace(cfg.Upstream.RetryBackoff)
	cfg.Upstream.HealthCheckInterval = strings.TrimSpace(cfg.Upstream.HealthCheckInterval)
	cfg.Upstream.HealthCheckTimeout = strings.TrimSpace(cfg.Upstream.HealthCheckTimeout)
	cfg.Upstream.BreakerInterval = strings.TrimSpace(cfg.Upstream.BreakerInterval)
	cfg.Upstream.BreakerTimeout = strings.TrimSpace(cfg.Upstream.BreakerTimeout)
	for i := range cfg.Auth.Tokens {
		token := &cfg.Auth.Tokens[i]
		token.Name = strings.TrimSpace(token.Name)
		token.Tenant = strings.TrimSpace(token.Tenant)
		token.App = strings.TrimSpace(token.App)
		token.ValueEnv = strings.TrimSpace(token.ValueEnv)
		token.Value = strings.TrimSpace(token.Value)
	}
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

func resolveStructuredTokens(cfg *Config) error {
	for i := range cfg.Auth.Tokens {
		token := &cfg.Auth.Tokens[i]
		if token.Disabled {
			continue
		}
		if strings.TrimSpace(token.Value) != "" {
			return fmt.Errorf("auth.tokens[%d] (%s) must not set inline value; use value_env instead", i, token.Name)
		}
		if token.ValueEnv == "" {
			return fmt.Errorf("auth.tokens[%d] (%s) must set value_env", i, token.Name)
		}
		envValue, ok := os.LookupEnv(token.ValueEnv)
		if !ok || strings.TrimSpace(envValue) == "" {
			return fmt.Errorf("auth.tokens[%d] (%s) requires environment variable %s", i, token.Name, token.ValueEnv)
		}
		token.Value = strings.TrimSpace(envValue)
	}
	return nil
}

func resolveProviderAPIKeys(cfg *Config) error {
	for i := range cfg.Providers {
		provider := &cfg.Providers[i]
		if strings.TrimSpace(provider.APIKey) != "" {
			return fmt.Errorf("provider %q still uses inline api_key in config file; remove it and use api_key_env instead", provider.Name)
		}
		envName := strings.TrimSpace(provider.APIKeyEnv)
		if envName == "" {
			continue
		}
		envValue, ok := os.LookupEnv(envName)
		if !ok || strings.TrimSpace(envValue) == "" {
			return fmt.Errorf("provider %q requires environment variable %s, but it is not set", provider.Name, envName)
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

func resolveAdminConfig(cfg *Config) error {
	if strings.TrimSpace(cfg.Auth.Admin.BearerToken) != "" {
		return fmt.Errorf("auth.admin.bearer_token must not be set in config file; use auth.admin.bearer_token_env instead")
	}
	envName := strings.TrimSpace(cfg.Auth.Admin.BearerTokenEnv)
	if envName != "" {
		envValue, ok := os.LookupEnv(envName)
		if ok && strings.TrimSpace(envValue) != "" {
			cfg.Auth.Admin.BearerToken = strings.TrimSpace(envValue)
		} else {
			log.Printf(
				"admin bearer token env %s is not set; admin endpoints will rely on CIDR rules or remain disabled",
				envName,
			)
		}
	}
	if cfg.Auth.Admin.RateLimitRPS <= 0 {
		cfg.Auth.Admin.RateLimitRPS = 2
	}
	if cfg.Auth.Admin.RateLimitBurst <= 0 {
		cfg.Auth.Admin.RateLimitBurst = 4
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
	for _, durationValue := range []struct{ name, value string }{
		{"server.read_timeout", cfg.Server.ReadTimeout},
		{"server.read_header_timeout", cfg.Server.ReadHeaderTimeout},
		{"server.write_timeout", cfg.Server.WriteTimeout},
		{"server.idle_timeout", cfg.Server.IdleTimeout},
		{"server.shutdown_timeout", cfg.Server.ShutdownTimeout},
		{"cache.ttl", cfg.Cache.TTL},
		{"upstream.retry_backoff", cfg.Upstream.RetryBackoff},
		{"upstream.health_check_interval", cfg.Upstream.HealthCheckInterval},
		{"upstream.health_check_timeout", cfg.Upstream.HealthCheckTimeout},
		{"upstream.breaker_interval", cfg.Upstream.BreakerInterval},
		{"upstream.breaker_timeout", cfg.Upstream.BreakerTimeout},
	} {
		if err := validatePositiveDuration(durationValue.name, durationValue.value); err != nil {
			return err
		}
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
	if len(cfg.Auth.ValidTokens) == 0 && len(cfg.Auth.Tokens) == 0 {
		return fmt.Errorf("at least one gateway token must be configured via auth.valid_tokens, auth.valid_tokens_env, or auth.tokens")
	}
	if cfg.Cache.MaxPayloadBytes <= 0 {
		return fmt.Errorf("cache.max_payload_bytes must be > 0")
	}
	if !strings.HasPrefix(cfg.Metrics.Path, "/") {
		return fmt.Errorf("metrics.path must start with /")
	}
	if !strings.HasPrefix(cfg.Debug.PprofPathPrefix, "/") {
		return fmt.Errorf("debug.pprof_path_prefix must start with /")
	}
	if cfg.Metrics.RateLimitRPS <= 0 {
		return fmt.Errorf("metrics.rate_limit_rps must be > 0")
	}
	if cfg.Metrics.RateLimitBurst <= 0 {
		return fmt.Errorf("metrics.rate_limit_burst must be > 0")
	}
	if cfg.Auth.Admin.RateLimitRPS <= 0 {
		return fmt.Errorf("auth.admin.rate_limit_rps must be > 0")
	}
	if cfg.Auth.Admin.RateLimitBurst <= 0 {
		return fmt.Errorf("auth.admin.rate_limit_burst must be > 0")
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
	for _, cidr := range cfg.Auth.Admin.AllowedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid auth.admin allowed cidr %q: %w", cidr, err)
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
	return validateTokenCatalog(cfg.Auth)
}

func validateTokenCatalog(auth AuthConfig) error {
	seenNames := make(map[string]struct{}, len(auth.Tokens))
	seenValues := make(map[string]struct{}, len(auth.Tokens)+len(auth.ValidTokens))
	for _, token := range auth.ValidTokens {
		normalized := strings.TrimSpace(token)
		if normalized == "" {
			continue
		}
		if _, ok := seenValues[normalized]; ok {
			return fmt.Errorf("duplicate gateway token found in auth.valid_tokens")
		}
		seenValues[normalized] = struct{}{}
	}
	for i, token := range auth.Tokens {
		if token.Disabled {
			continue
		}
		if token.Name == "" {
			return fmt.Errorf("auth.tokens[%d].name cannot be empty", i)
		}
		if _, ok := seenNames[token.Name]; ok {
			return fmt.Errorf("duplicate auth token name: %s", token.Name)
		}
		seenNames[token.Name] = struct{}{}
		if token.Value == "" {
			return fmt.Errorf("auth.tokens[%d] (%s) resolved empty token value", i, token.Name)
		}
		if _, ok := seenValues[token.Value]; ok {
			return fmt.Errorf("duplicate gateway token material detected for auth.tokens[%d] (%s)", i, token.Name)
		}
		seenValues[token.Value] = struct{}{}
		if token.RateLimitQPS < 0 {
			return fmt.Errorf("auth.tokens[%d] (%s) rate_limit_qps must be >= 0", i, token.Name)
		}
		if token.RateLimitBurst < 0 {
			return fmt.Errorf("auth.tokens[%d] (%s) rate_limit_burst must be >= 0", i, token.Name)
		}
		if token.DailyTokenLimit < 0 {
			return fmt.Errorf("auth.tokens[%d] (%s) daily_token_limit must be >= 0", i, token.Name)
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
