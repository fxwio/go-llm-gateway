package config

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Server    ServerConfig     `mapstructure:"server"`
	Redis     RedisConfig      `mapstructure:"redis"`
	Auth      AuthConfig       `mapstructure:"auth"`
	Providers []ProviderConfig `mapstructure:"providers"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type AuthConfig struct {
	ValidTokens    []string `mapstructure:"valid_tokens"`
	RateLimitQPS   float64  `mapstructure:"rate_limit_qps"`
	RateLimitBurst int      `mapstructure:"rate_limit_burst"`
}

type ServerConfig struct {
	Port         int    `mapstructure:"port"`
	ReadTimeout  string `mapstructure:"read_timeout"`
	WriteTimeout string `mapstructure:"write_timeout"`
}

type ProviderConfig struct {
	Name      string   `mapstructure:"name"`
	BaseURL   string   `mapstructure:"base_url"`
	APIKeyEnv string   `mapstructure:"api_key_env"`
	APIKey    string   `mapstructure:"api_key"`
	Models    []string `mapstructure:"models"`
}

var GlobalConfig *Config

func LoadConfig(path string) {
	viper.SetConfigFile(path)
	viper.SetConfigType("yaml")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("error reading config file: %v", err)
	}

	cfg := &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		log.Fatalf("unable to decode config: %v", err)
	}

	if err := resolveProviderAPIKeys(cfg); err != nil {
		log.Fatalf("failed to resolve provider api keys: %v", err)
	}

	if err := validateConfig(cfg); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	GlobalConfig = cfg
	log.Printf("Configuration loaded successfully. Loaded %d providers.", len(GlobalConfig.Providers))
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
			// 允许某些本地/无鉴权的 OpenAI-compatible provider 不配置 key
			// 例如本地 vLLM / Ollama / 内网网关
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

func validateConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	if len(cfg.Providers) == 0 {
		return fmt.Errorf("at least one provider must be configured")
	}

	seenProviders := make(map[string]struct{}, len(cfg.Providers))

	for _, provider := range cfg.Providers {
		name := strings.TrimSpace(provider.Name)
		if name == "" {
			return fmt.Errorf("provider name cannot be empty")
		}

		if _, exists := seenProviders[name]; exists {
			return fmt.Errorf("duplicate provider name: %s", name)
		}
		seenProviders[name] = struct{}{}

		if strings.TrimSpace(provider.BaseURL) == "" {
			return fmt.Errorf("provider %q base_url cannot be empty", name)
		}

		if len(provider.Models) == 0 {
			return fmt.Errorf("provider %q must configure at least one model", name)
		}
	}

	return nil
}
