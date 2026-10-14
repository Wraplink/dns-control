package config

import (
	"fmt"
	"os"
	"time"

	"github.com/wraplink/dns-control/internal/domain"
	"gopkg.in/yaml.v3"
)

type Config struct {
	DNS   DNSConfig   `yaml:"dns"`
	API   APIConfig   `yaml:"api"`
	Agent AgentConfig `yaml:"agent"`

	Domains []domain.Rule `yaml:"domains"`
}

type DNSConfig struct {
	Listen   string   `yaml:"listen"`
	Upstream []string `yaml:"upstream"`
	TTL      uint32   `yaml:"ttl"`
}

type APIConfig struct {
	Listen string `yaml:"listen"`
}

type AgentConfig struct {
	Token             string        `yaml:"token"`
	LeaseDuration     time.Duration `yaml:"lease_duration"`
	OfflineAfter      time.Duration `yaml:"offline_after"`
	ReconcileInterval time.Duration `yaml:"reconcile_interval"`
}

func Load(filename string) (Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", filename, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", filename, err)
	}

	if cfg.Agent.LeaseDuration == 0 {
		cfg.Agent.LeaseDuration = 30 * time.Second
	}
	if cfg.Agent.OfflineAfter == 0 {
		cfg.Agent.OfflineAfter = 45 * time.Second
	}
	if cfg.Agent.ReconcileInterval == 0 {
		cfg.Agent.ReconcileInterval = 5 * time.Second
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.DNS.Listen == "" {
		return fmt.Errorf("dns.listen is required")
	}

	for i, upstream := range c.DNS.Upstream {
		if upstream == "" {
			return fmt.Errorf("dns.upstream[%d] cannot be empty", i)
		}
	}

	if c.DNS.TTL == 0 {
		return fmt.Errorf("dns.ttl must be greater than zero")
	}
	if c.API.Listen == "" {
		return fmt.Errorf("api.listen is required")
	}
	if c.Agent.LeaseDuration <= 0 {
		return fmt.Errorf("agent.lease_duration must be greater than zero")
	}
	if c.Agent.OfflineAfter <= 0 {
		return fmt.Errorf("agent.offline_after must be greater than zero")
	}
	if c.Agent.OfflineAfter < c.Agent.LeaseDuration {
		return fmt.Errorf("agent.offline_after must be greater than or equal to lease_duration")
	}
	if c.Agent.ReconcileInterval <= 0 {
		return fmt.Errorf("agent.reconcile_interval must be greater than zero")
	}

	return nil
}
