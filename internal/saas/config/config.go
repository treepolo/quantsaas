package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	AppRoleSaaS = "saas"
	AppRoleLab  = "lab"
	AppRoleDev  = "dev"
)

type Config struct {
	AppRole  string         `yaml:"app_role"`
	Database DatabaseConfig `yaml:"database"`
	Redis    RedisConfig    `yaml:"redis"`
	Compute  ComputeConfig  `yaml:"compute"`
	JWT      JWTConfig      `yaml:"jwt"`
	Server   ServerConfig   `yaml:"server"`
}

type DatabaseConfig struct {
	DSN          string `yaml:"dsn"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
	MaxOpenConns int    `yaml:"max_open_conns"`
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type ComputeConfig struct {
	Workers          int `yaml:"workers"`
	SoftItemLimit    int `yaml:"soft_item_limit"`
	HardItemLimit    int `yaml:"hard_item_limit"`
	LeaseSeconds     int `yaml:"lease_seconds"`
	PollMilliseconds int `yaml:"poll_milliseconds"`
}

type JWTConfig struct {
	Secret     string `yaml:"secret"`
	Issuer     string `yaml:"issuer"`
	TTLMinutes int    `yaml:"ttl_minutes"`
}

type ServerConfig struct {
	Addr                string `yaml:"addr"`
	ReadTimeoutSeconds  int    `yaml:"read_timeout_seconds"`
	WriteTimeoutSeconds int    `yaml:"write_timeout_seconds"`
}

func Default() Config {
	return Config{
		AppRole: AppRoleDev,
		Database: DatabaseConfig{
			MaxIdleConns: 5,
			MaxOpenConns: 25,
		},
		Redis: RedisConfig{
			Addr: "127.0.0.1:6379",
			DB:   0,
		},
		Compute: ComputeConfig{
			Workers:          4,
			SoftItemLimit:    1000,
			HardItemLimit:    300000,
			LeaseSeconds:     60,
			PollMilliseconds: 250,
		},
		JWT: JWTConfig{
			Issuer:     "quantsaas",
			TTLMinutes: 1440,
		},
		Server: ServerConfig{
			Addr:                ":8080",
			ReadTimeoutSeconds:  15,
			WriteTimeoutSeconds: 30,
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()

	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config file: %w", err)
		}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config file: %w", err)
		}
	}

	applyEnv(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	switch c.AppRole {
	case AppRoleSaaS, AppRoleLab, AppRoleDev:
	default:
		return fmt.Errorf("invalid app_role %q", c.AppRole)
	}

	if strings.TrimSpace(c.Database.DSN) == "" {
		return errors.New("database.dsn is required; set DATABASE_DSN")
	}
	if strings.TrimSpace(c.JWT.Secret) == "" {
		return errors.New("jwt.secret is required; set JWT_SECRET")
	}
	if c.JWT.TTLMinutes <= 0 {
		return errors.New("jwt.ttl_minutes must be positive")
	}
	if strings.TrimSpace(c.Redis.Addr) == "" {
		return errors.New("redis.addr is required")
	}
	if c.Compute.Workers < 1 || c.Compute.Workers > 64 {
		return errors.New("compute.workers must be between 1 and 64")
	}
	if c.Compute.SoftItemLimit < 1 || c.Compute.HardItemLimit < c.Compute.SoftItemLimit {
		return errors.New("compute item limits must be positive and hard_item_limit must not be smaller than soft_item_limit")
	}
	if c.Compute.LeaseSeconds < 5 || c.Compute.PollMilliseconds < 10 {
		return errors.New("compute lease_seconds and poll_milliseconds are too small")
	}
	if strings.TrimSpace(c.Server.Addr) == "" {
		return errors.New("server.addr is required")
	}
	return nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("APP_ROLE"); v != "" {
		cfg.AppRole = strings.ToLower(strings.TrimSpace(v))
	}
	if v := os.Getenv("DATABASE_DSN"); v != "" {
		cfg.Database.DSN = v
	}
	if v := os.Getenv("REDIS_ADDR"); v != "" {
		cfg.Redis.Addr = v
	}
	if v := os.Getenv("REDIS_PASSWORD"); v != "" {
		cfg.Redis.Password = v
	}
	if v := os.Getenv("REDIS_DB"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.Redis.DB = parsed
		}
	}
	if v := os.Getenv("COMPUTE_WORKERS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.Compute.Workers = parsed
		}
	}
	if v := os.Getenv("COMPUTE_SOFT_ITEM_LIMIT"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.Compute.SoftItemLimit = parsed
		}
	}
	if v := os.Getenv("COMPUTE_HARD_ITEM_LIMIT"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.Compute.HardItemLimit = parsed
		}
	}
	if v := os.Getenv("COMPUTE_LEASE_SECONDS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.Compute.LeaseSeconds = parsed
		}
	}
	if v := os.Getenv("COMPUTE_POLL_MILLISECONDS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.Compute.PollMilliseconds = parsed
		}
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		cfg.JWT.Secret = v
	}
	if v := os.Getenv("SERVER_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
}
