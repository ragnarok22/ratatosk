package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

var osUserHomeDir = os.UserHomeDir

// ServerConfig holds all configuration for the relay server.
type ServerConfig struct {
	BaseDomain     string `mapstructure:"base_domain"`
	PublicPort     int    `mapstructure:"public_port"`
	AdminPort      int    `mapstructure:"admin_port"`
	ControlPort    int    `mapstructure:"control_port"`
	TLSEnabled     bool   `mapstructure:"tls_enabled"`
	TLSCertFile    string `mapstructure:"tls_cert_file"`
	TLSKeyFile     string `mapstructure:"tls_key_file"`
	TLSAuto        bool   `mapstructure:"tls_auto"`
	TLSEmail       string `mapstructure:"tls_email"`
	TLSProvider    string `mapstructure:"tls_provider"`
	TLSAPIToken    string `mapstructure:"tls_api_token"`
	PortRangeStart int    `mapstructure:"port_range_start"`
	PortRangeEnd   int    `mapstructure:"port_range_end"`
}

// PublicAddr returns the listen address for the public HTTP(S) server.
func (c *ServerConfig) PublicAddr() string {
	return fmt.Sprintf(":%d", c.PublicPort)
}

// AdminAddr returns the listen address for the admin dashboard.
func (c *ServerConfig) AdminAddr() string {
	return fmt.Sprintf(":%d", c.AdminPort)
}

// ControlAddr returns the listen address for the TCP control plane.
func (c *ServerConfig) ControlAddr() string {
	return fmt.Sprintf(":%d", c.ControlPort)
}

// TunnelURL returns the full URL for a given subdomain.
func (c *ServerConfig) TunnelURL(subdomain string) string {
	scheme := "http"
	if c.TLSEnabled || c.TLSAuto {
		scheme = "https"
	}
	host := subdomain + "." + c.BaseDomain
	// Omit port for standard ports.
	if (scheme == "http" && c.PublicPort == 80) || (scheme == "https" && c.PublicPort == 443) {
		return fmt.Sprintf("%s://%s", scheme, host)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, c.PublicPort)
}

// Validate checks for invalid or conflicting TLS configuration.
func (c *ServerConfig) Validate() error {
	if c.TLSAuto && c.TLSEnabled {
		return fmt.Errorf("tls_auto and tls_enabled are mutually exclusive")
	}
	if c.TLSAuto {
		if c.TLSEmail == "" {
			return fmt.Errorf("tls_email is required when tls_auto is enabled")
		}
		if c.TLSProvider == "" {
			return fmt.Errorf("tls_provider is required when tls_auto is enabled")
		}
		if c.TLSAPIToken == "" {
			return fmt.Errorf("tls_api_token is required when tls_auto is enabled")
		}
		if c.BaseDomain == "localhost" {
			return fmt.Errorf("tls_auto requires a real base_domain, not localhost")
		}
	}
	if c.TLSEnabled {
		if c.TLSCertFile == "" || c.TLSKeyFile == "" {
			return fmt.Errorf("tls_cert_file and tls_key_file are required when tls_enabled is true")
		}
	}
	return nil
}

// LoadConfig reads configuration from file, environment variables, and defaults.
// Search paths for ratatosk.yaml: /etc/ratatosk/, <resolved-home>/.ratatosk/,
// and the current working directory. The home path is resolved via
// os.UserHomeDir(); if it cannot be resolved, LoadConfig returns an error.
// Environment variables use the RATATOSK_ prefix (e.g. RATATOSK_BASE_DOMAIN).
func LoadConfig() (*ServerConfig, error) {
	v := viper.New()

	// Defaults.
	v.SetDefault("base_domain", "localhost")
	v.SetDefault("public_port", 8080)
	v.SetDefault("admin_port", 8081)
	v.SetDefault("control_port", 7000)
	v.SetDefault("tls_enabled", false)
	v.SetDefault("tls_cert_file", "")
	v.SetDefault("tls_key_file", "")
	v.SetDefault("tls_auto", false)
	v.SetDefault("tls_email", "")
	v.SetDefault("tls_provider", "")
	v.SetDefault("tls_api_token", "")
	v.SetDefault("port_range_start", 10000)
	v.SetDefault("port_range_end", 20000)

	// Config file search.
	v.SetConfigName("ratatosk")
	v.SetConfigType("yaml")
	v.AddConfigPath("/etc/ratatosk/")
	homeDir, err := osUserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home directory: %w", err)
	}
	v.AddConfigPath(filepath.Join(homeDir, ".ratatosk"))
	v.AddConfigPath(".")

	// Environment variables.
	v.SetEnvPrefix("RATATOSK")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Read config file (optional — defaults and env vars are sufficient).
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
		slog.Info("no config file found, using defaults and environment variables")
	} else {
		slog.Info("loaded config file", "path", v.ConfigFileUsed())
	}

	var cfg ServerConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}
