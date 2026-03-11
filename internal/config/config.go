package config

import (
	"log"
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
	Port        int    `mapstructure:"port"`
	ReadTimeout string `mapstructure:"read_timeout"`
}

type ProviderConfig struct {
	Name    string   `mapstructure:"name"`
	BaseURL string   `mapstructure:"base_url"`
	APIKey  string   `mapstructure:"api_key"`
	Models  []string `mapstructure:"models"`
}

var GlobalConfig *Config

func LoadConfig(path string) {
	viper.SetConfigFile(path)
	viper.SetConfigType("yaml")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}

	GlobalConfig = &Config{}
	if err := viper.Unmarshal(GlobalConfig); err != nil {
		log.Fatalf("Unable to decode into struct: %v", err)
	}

	log.Printf("Configuration loaded successfully. Loaded %d providers.", len(GlobalConfig.Providers))
}
