package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// ServerConfig holds all configuration for the relay server.
type ServerConfig struct {
	BaseDomain     string `mapstructure:"base_domain"`
	PublicPort     int    `mapstructure:"public_port"`
	AdminPort      int    `mapstructure:"admin_port"`
	ControlPort    int    `mapstructure:"control_port"`
	TLSEnabled     bool   `mapstructure:"tls_enabled"`
	TLSCertFile    string `mapstructure:"tls_cert_file"`
	TLSKeyFile     string `mapstructure:"tls_key_file"`
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
	if c.TLSEnabled {
		scheme = "https"
	}
	host := subdomain + "." + c.BaseDomain
	// Omit port for standard ports.
	if (scheme == "http" && c.PublicPort == 80) || (scheme == "https" && c.PublicPort == 443) {
		return fmt.Sprintf("%s://%s", scheme, host)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, c.PublicPort)
}

// LoadConfig reads configuration from file, environment variables, and defaults.
// Search paths for ratatosk.yaml: /etc/ratatosk/, $HOME/.ratatosk/, and the
// current working directory. Environment variables use the RATATOSK_ prefix
// (e.g. RATATOSK_BASE_DOMAIN).
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
	v.SetDefault("port_range_start", 10000)
	v.SetDefault("port_range_end", 20000)

	// Config file search.
	v.SetConfigName("ratatosk")
	v.SetConfigType("yaml")
	v.AddConfigPath("/etc/ratatosk/")
	if homeDir, err := os.UserHomeDir(); err == nil && homeDir != "" {
		v.AddConfigPath(filepath.Join(homeDir, ".ratatosk"))
	}
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

	return &cfg, nil
}
