package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Telegram   TelegramConfig   `yaml:"telegram"`
	Rspamd     RspamdConfig     `yaml:"rspamd"`
	Redis      RedisConfig      `yaml:"redis"`
	ClickHouse ClickHouseConfig `yaml:"clickhouse"`
	Thresholds ThresholdsConfig `yaml:"thresholds"`
	Maps       MapsConfig       `yaml:"maps"`
}

type TelegramConfig struct {
	MonitoredChats   []int64 `yaml:"monitored_chats"`
	ModeratorChannel int64   `yaml:"moderator_channel"`
}

type MapsConfig struct {
	Dir string `yaml:"dir"`
}

type RspamdConfig struct {
	URL      string        `yaml:"url"`
	Password string        `yaml:"password"`
	Timeout  time.Duration `yaml:"timeout"`
}

type RspamdConfigRaw struct {
	URL      string `yaml:"url"`
	Password string `yaml:"password"`
	Timeout  string `yaml:"timeout"`
}

type RedisConfig struct {
	Addr string `yaml:"addr"`
	DB   int    `yaml:"db"`
}

type ClickHouseConfig struct {
	DSN string `yaml:"dsn"`
}

type ThresholdsConfig struct {
	LogScore    float64 `yaml:"log_score"`
	RejectScore float64 `yaml:"reject_score"`
}

// envVarRe matches ${VAR_NAME} patterns
var envVarRe = regexp.MustCompile(`\$\{([^}]+)\}`)

// expandEnv replaces ${VAR} references with environment variable values
func expandEnv(s string) string {
	return envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		varName := envVarRe.FindStringSubmatch(match)[1]
		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		return match
	})
}

// Load reads config from a YAML file, expanding environment variable references.
// It loads .env file first if present.
func Load(path string) (*Config, error) {
	// Load .env file if it exists (ignore error if not found)
	_ = godotenv.Load()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	// Expand environment variables in YAML
	expanded := expandEnv(string(data))

	// First unmarshal to get raw timeout string
	var raw struct {
		Telegram   TelegramConfig   `yaml:"telegram"`
		Rspamd     RspamdConfigRaw  `yaml:"rspamd"`
		Redis      RedisConfig      `yaml:"redis"`
		ClickHouse ClickHouseConfig `yaml:"clickhouse"`
		Thresholds ThresholdsConfig `yaml:"thresholds"`
		Maps       MapsConfig       `yaml:"maps"`
	}

	if err := yaml.Unmarshal([]byte(expanded), &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	timeout, err := time.ParseDuration(raw.Rspamd.Timeout)
	if err != nil {
		timeout = 10 * time.Second
	}

	cfg := &Config{
		Telegram: raw.Telegram,
		Rspamd: RspamdConfig{
			URL:      raw.Rspamd.URL,
			Password: raw.Rspamd.Password,
			Timeout:  timeout,
		},
		Redis:      raw.Redis,
		ClickHouse: raw.ClickHouse,
		Thresholds: raw.Thresholds,
		Maps:       raw.Maps,
	}

	return cfg, nil
}
