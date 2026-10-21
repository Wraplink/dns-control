package config

import (
	"fmt"
	"net"
	"os"
	"strings"
	"net/url"
	"time"

	"github.com/wraplink/dns-control/internal/domain"
	"gopkg.in/yaml.v3"
)

type Config struct {
	DNS     DNSConfig     `yaml:"dns"`
	API     APIConfig     `yaml:"api"`
	Agent   AgentConfig   `yaml:"agent"`
	Backend BackendConfig `yaml:"backend"`
	Node    NodeConfig    `yaml:"node"`

	Domains []domain.Rule `yaml:"domains"`
}

type DNSConfig struct {
	Listen   string   `yaml:"listen"`
	Upstream []string `yaml:"upstream"`
	TTL      uint32   `yaml:"ttl"`
	ParentURL string `yaml:"parent_url"`
	ParentToken string `yaml:"parent_token"`
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

type BackendConfig struct {
	URL                        string        `yaml:"url"`
	ControlKey                 string        `yaml:"control_key"`
	EntitlementRefreshInterval time.Duration `yaml:"entitlement_refresh_interval"`
	EntitlementMaxStale        time.Duration `yaml:"entitlement_max_stale"`
}

type NodeConfig struct {
	ID               string        `yaml:"id"`
	Region           string        `yaml:"region"`
	PublicDNS        []string      `yaml:"public_dns"`
	RegisterInterval time.Duration `yaml:"register_interval"`
}

func (c BackendConfig) Enabled() bool {
	return strings.TrimSpace(c.URL) != ""
}

func (c NodeConfig) Enabled() bool {
	return strings.TrimSpace(c.ID) != "" || len(c.PublicDNS) > 0
}

func Load(filename string) (Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", filename, err)
	}

	data = []byte(os.ExpandEnv(string(data)))

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", filename, err)
	}

	cfg.Backend.URL = strings.TrimRight(strings.TrimSpace(cfg.Backend.URL), "/")
	cfg.Backend.ControlKey = strings.TrimSpace(cfg.Backend.ControlKey)
	cfg.Node.ID = strings.TrimSpace(cfg.Node.ID)
	cfg.Node.Region = strings.TrimSpace(cfg.Node.Region)

	if cfg.Agent.LeaseDuration == 0 {
		cfg.Agent.LeaseDuration = 30 * time.Second
	}
	if cfg.Agent.OfflineAfter == 0 {
		cfg.Agent.OfflineAfter = 45 * time.Second
	}
	if cfg.Agent.ReconcileInterval == 0 {
		cfg.Agent.ReconcileInterval = 5 * time.Second
	}
	if cfg.Backend.EntitlementRefreshInterval == 0 {
		cfg.Backend.EntitlementRefreshInterval = 10 * time.Second
	}
	if cfg.Backend.EntitlementMaxStale == 0 {
		cfg.Backend.EntitlementMaxStale = time.Minute
	}
	if cfg.Node.RegisterInterval == 0 {
		cfg.Node.RegisterInterval = 15 * time.Second
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
	if c.DNS.ParentURL != "" {
		u, err := url.Parse(c.DNS.ParentURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("dns.parent_url must be an HTTPS URL without credentials or query")
		}
		if len(c.DNS.ParentToken) < 32 {
			return fmt.Errorf("dns.parent_token must contain at least 32 characters")
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

	backendURL := strings.TrimSpace(c.Backend.URL)
	backendKey := strings.TrimSpace(c.Backend.ControlKey)
	if (backendURL == "") != (backendKey == "") {
		return fmt.Errorf("backend.url and backend.control_key must be configured together")
	}
	if backendURL != "" {
		if len(backendKey) < 32 {
			return fmt.Errorf("backend.control_key must contain at least 32 characters")
		}
		if c.Backend.EntitlementRefreshInterval <= 0 {
			return fmt.Errorf("backend.entitlement_refresh_interval must be greater than zero")
		}
		if c.Backend.EntitlementMaxStale < c.Backend.EntitlementRefreshInterval {
			return fmt.Errorf("backend.entitlement_max_stale must be greater than or equal to entitlement_refresh_interval")
		}
	}

	if c.Node.Enabled() {
		if backendURL == "" {
			return fmt.Errorf("node registration requires backend.url and backend.control_key")
		}
		if c.Node.ID == "" || len(c.Node.ID) > 128 {
			return fmt.Errorf("node.id is required and must contain at most 128 characters")
		}
		if len(c.Node.PublicDNS) == 0 {
			return fmt.Errorf("node.public_dns must contain at least one DNS IP address")
		}
		for i, address := range c.Node.PublicDNS {
			address = strings.TrimSpace(address)
			if address == "" || net.ParseIP(address) == nil {
				return fmt.Errorf("node.public_dns[%d] must be a valid IP address", i)
			}
		}
		if c.Node.RegisterInterval <= 0 {
			return fmt.Errorf("node.register_interval must be greater than zero")
		}
	}

	return nil
}
