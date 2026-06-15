package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type AgentConfig struct {
	SaaSURL  string         `yaml:"saas_url"`
	Email    string         `yaml:"email"`
	Password string         `yaml:"password"`
	Exchange ExchangeConfig `yaml:"exchange"`
}

type ExchangeConfig struct {
	Name      string `yaml:"name"`
	APIKey    string `yaml:"api_key"`
	SecretKey string `yaml:"secret_key"`
	Sandbox   bool   `yaml:"sandbox"`
	BaseURL   string `yaml:"base_url"`
}

func Load(path string) (AgentConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return AgentConfig{}, fmt.Errorf("讀取 Agent 設定失敗: %w", err)
	}
	var cfg AgentConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return AgentConfig{}, fmt.Errorf("解析 Agent 設定失敗: %w", err)
	}
	if cfg.SaaSURL == "" || cfg.Email == "" || cfg.Password == "" {
		return AgentConfig{}, fmt.Errorf("saas_url、email、password 皆為必填")
	}
	if cfg.Exchange.Name == "" {
		cfg.Exchange.Name = "binance"
	}
	return cfg, nil
}
