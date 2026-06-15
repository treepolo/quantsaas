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
	if v := os.Getenv("JWT_SECRET"); v != "" {
		cfg.JWT.Secret = v
	}
	if v := os.Getenv("SERVER_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
}
