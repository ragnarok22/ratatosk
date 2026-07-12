package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

var osUserHomeDir = os.UserHomeDir

// ServerConfig holds all configuration for the relay server.
type ServerConfig struct {
	BaseDomain         string `mapstructure:"base_domain"`
	PublicPort         int    `mapstructure:"public_port"`
	AdminHost          string `mapstructure:"admin_host"`
	AdminPort          int    `mapstructure:"admin_port"`
	AdminUsername      string `mapstructure:"admin_username"`
	AdminPassword      string `mapstructure:"admin_password"`
	AdminTLSEnabled    bool   `mapstructure:"admin_tls_enabled"`
	AdminTLSCertFile   string `mapstructure:"admin_tls_cert_file"`
	AdminTLSKeyFile    string `mapstructure:"admin_tls_key_file"`
	ControlHost        string `mapstructure:"control_host"`
	ControlPort        int    `mapstructure:"control_port"`
	ControlToken       string `mapstructure:"control_token"`
	ControlTLSEnabled  bool   `mapstructure:"control_tls_enabled"`
	ControlTLSCertFile string `mapstructure:"control_tls_cert_file"`
	ControlTLSKeyFile  string `mapstructure:"control_tls_key_file"`
	TLSEnabled         bool   `mapstructure:"tls_enabled"`
	TLSCertFile        string `mapstructure:"tls_cert_file"`
	TLSKeyFile         string `mapstructure:"tls_key_file"`
	TLSAuto            bool   `mapstructure:"tls_auto"`
	TLSEmail           string `mapstructure:"tls_email"`
	TLSProvider        string `mapstructure:"tls_provider"`
	TLSAPIToken        string `mapstructure:"tls_api_token"`
	PortRangeStart     int    `mapstructure:"port_range_start"`
	PortRangeEnd       int    `mapstructure:"port_range_end"`
}

// PublicAddr returns the listen address for the public HTTP(S) server.
func (c *ServerConfig) PublicAddr() string {
	return fmt.Sprintf(":%d", c.PublicPort)
}

// AdminAddr returns the listen address for the admin dashboard.
func (c *ServerConfig) AdminAddr() string {
	return net.JoinHostPort(c.AdminHost, strconv.Itoa(c.AdminPort))
}

// ControlAddr returns the listen address for the TCP control plane.
func (c *ServerConfig) ControlAddr() string {
	return net.JoinHostPort(c.ControlHost, strconv.Itoa(c.ControlPort))
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

// Validate checks for invalid or conflicting server configuration.
func (c *ServerConfig) Validate() error {
	if !validBaseDomain(c.BaseDomain) {
		return fmt.Errorf("base_domain must be localhost or a valid DNS name")
	}
	if c.AdminHost == "" {
		return fmt.Errorf("admin_host must not be empty")
	}
	if c.ControlHost == "" {
		return fmt.Errorf("control_host must not be empty")
	}
	if (c.AdminUsername == "") != (c.AdminPassword == "") {
		return fmt.Errorf("admin_username and admin_password must be configured together")
	}
	if c.AdminTLSEnabled && (c.AdminTLSCertFile == "" || c.AdminTLSKeyFile == "") {
		return fmt.Errorf("admin_tls_cert_file and admin_tls_key_file are required when admin_tls_enabled is true")
	}
	if !isLoopbackHost(c.AdminHost) {
		if c.AdminUsername == "" {
			return fmt.Errorf("admin_username and admin_password are required for a non-loopback admin_host")
		}
		if !c.AdminTLSEnabled {
			return fmt.Errorf("admin_tls_enabled must be true for a non-loopback admin_host")
		}
	}
	if c.ControlTLSEnabled && (c.ControlTLSCertFile == "" || c.ControlTLSKeyFile == "") {
		return fmt.Errorf("control_tls_cert_file and control_tls_key_file are required when control_tls_enabled is true")
	}
	if !isLoopbackHost(c.ControlHost) {
		if c.ControlToken == "" {
			return fmt.Errorf("control_token is required for a non-loopback control_host")
		}
		if !c.ControlTLSEnabled {
			return fmt.Errorf("control_tls_enabled must be true for a non-loopback control_host")
		}
	}

	servicePorts := []struct {
		name string
		port int
	}{
		{name: "public_port", port: c.PublicPort},
		{name: "admin_port", port: c.AdminPort},
		{name: "control_port", port: c.ControlPort},
	}
	seenPorts := make(map[int]string, len(servicePorts))
	for _, service := range servicePorts {
		if service.port < 1 || service.port > 65535 {
			return fmt.Errorf("%s must be between 1 and 65535", service.name)
		}
		if other, ok := seenPorts[service.port]; ok {
			return fmt.Errorf("%s conflicts with %s on port %d", service.name, other, service.port)
		}
		seenPorts[service.port] = service.name
	}

	if c.PortRangeStart < 1 || c.PortRangeStart > 65535 {
		return fmt.Errorf("port_range_start must be between 1 and 65535")
	}
	if c.PortRangeEnd < 1 || c.PortRangeEnd > 65535 {
		return fmt.Errorf("port_range_end must be between 1 and 65535")
	}
	if c.PortRangeStart >= c.PortRangeEnd {
		return fmt.Errorf("port range must be nonempty: port_range_start must be less than port_range_end")
	}
	for _, service := range servicePorts {
		if service.port >= c.PortRangeStart && service.port < c.PortRangeEnd {
			return fmt.Errorf("%s conflicts with tunnel port range [%d, %d)", service.name, c.PortRangeStart, c.PortRangeEnd)
		}
	}

	if c.TLSAuto && c.TLSEnabled {
		return fmt.Errorf("tls_auto and tls_enabled are mutually exclusive")
	}
	if c.TLSAuto {
		if c.PublicPort != 443 {
			return fmt.Errorf("public_port must be 443 when tls_auto is enabled")
		}
		if c.TLSEmail == "" {
			return fmt.Errorf("tls_email is required when tls_auto is enabled")
		}
		if c.TLSProvider != "cloudflare" {
			return fmt.Errorf("tls_provider must be cloudflare when tls_auto is enabled")
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

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validBaseDomain(domain string) bool {
	if domain == "localhost" {
		return true
	}
	if len(domain) == 0 || len(domain) > 253 || !strings.Contains(domain, ".") || strings.HasSuffix(domain, ".") || net.ParseIP(domain) != nil {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
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
	v.SetDefault("admin_host", "127.0.0.1")
	v.SetDefault("admin_port", 8081)
	v.SetDefault("admin_username", "")
	v.SetDefault("admin_password", "")
	v.SetDefault("admin_tls_enabled", false)
	v.SetDefault("admin_tls_cert_file", "")
	v.SetDefault("admin_tls_key_file", "")
	v.SetDefault("control_host", "127.0.0.1")
	v.SetDefault("control_port", 7000)
	v.SetDefault("control_token", "")
	v.SetDefault("control_tls_enabled", false)
	v.SetDefault("control_tls_cert_file", "")
	v.SetDefault("control_tls_key_file", "")
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
