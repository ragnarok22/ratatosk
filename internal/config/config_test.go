package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	// Ensure no config file or env vars interfere.
	for _, key := range []string{
		"RATATOSK_BASE_DOMAIN",
		"RATATOSK_PUBLIC_PORT",
		"RATATOSK_ADMIN_PORT",
		"RATATOSK_CONTROL_PORT",
		"RATATOSK_TLS_ENABLED",
	} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.BaseDomain != "localhost" {
		t.Errorf("BaseDomain = %q, want %q", cfg.BaseDomain, "localhost")
	}
	if cfg.PublicPort != 8080 {
		t.Errorf("PublicPort = %d, want %d", cfg.PublicPort, 8080)
	}
	if cfg.AdminPort != 8081 {
		t.Errorf("AdminPort = %d, want %d", cfg.AdminPort, 8081)
	}
	if cfg.ControlPort != 7000 {
		t.Errorf("ControlPort = %d, want %d", cfg.ControlPort, 7000)
	}
	if cfg.TLSEnabled {
		t.Error("TLSEnabled should be false by default")
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("RATATOSK_BASE_DOMAIN", "example.com")
	t.Setenv("RATATOSK_PUBLIC_PORT", "443")
	t.Setenv("RATATOSK_TLS_ENABLED", "true")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.BaseDomain != "example.com" {
		t.Errorf("BaseDomain = %q, want %q", cfg.BaseDomain, "example.com")
	}
	if cfg.PublicPort != 443 {
		t.Errorf("PublicPort = %d, want %d", cfg.PublicPort, 443)
	}
	if !cfg.TLSEnabled {
		t.Error("TLSEnabled should be true")
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`base_domain: tunnel.dev
public_port: 9090
admin_port: 9091
control_port: 7001
tls_enabled: true
tls_cert_file: /etc/ssl/cert.pem
tls_key_file: /etc/ssl/key.pem
`)
	if err := os.WriteFile(filepath.Join(dir, "ratatosk.yaml"), content, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Change to temp dir so viper finds the file via "." search path.
	orig, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(orig) })

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.BaseDomain != "tunnel.dev" {
		t.Errorf("BaseDomain = %q, want %q", cfg.BaseDomain, "tunnel.dev")
	}
	if cfg.PublicPort != 9090 {
		t.Errorf("PublicPort = %d, want %d", cfg.PublicPort, 9090)
	}
	if cfg.AdminPort != 9091 {
		t.Errorf("AdminPort = %d, want %d", cfg.AdminPort, 9091)
	}
	if cfg.ControlPort != 7001 {
		t.Errorf("ControlPort = %d, want %d", cfg.ControlPort, 7001)
	}
	if !cfg.TLSEnabled {
		t.Error("TLSEnabled should be true")
	}
	if cfg.TLSCertFile != "/etc/ssl/cert.pem" {
		t.Errorf("TLSCertFile = %q, want %q", cfg.TLSCertFile, "/etc/ssl/cert.pem")
	}
	if cfg.TLSKeyFile != "/etc/ssl/key.pem" {
		t.Errorf("TLSKeyFile = %q, want %q", cfg.TLSKeyFile, "/etc/ssl/key.pem")
	}
}

func TestLoadConfigInvalidFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ratatosk.yaml"), []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	orig, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(orig) })

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for invalid YAML file")
	}
}

func TestTunnelURL(t *testing.T) {
	tests := []struct {
		name      string
		cfg       ServerConfig
		subdomain string
		want      string
	}{
		{
			name:      "http with non-standard port",
			cfg:       ServerConfig{BaseDomain: "localhost", PublicPort: 8080},
			subdomain: "quick-fox-1234",
			want:      "http://quick-fox-1234.localhost:8080",
		},
		{
			name:      "http on port 80",
			cfg:       ServerConfig{BaseDomain: "example.com", PublicPort: 80},
			subdomain: "test",
			want:      "http://test.example.com",
		},
		{
			name:      "https on port 443",
			cfg:       ServerConfig{BaseDomain: "tunnel.dev", PublicPort: 443, TLSEnabled: true},
			subdomain: "app",
			want:      "https://app.tunnel.dev",
		},
		{
			name:      "https with non-standard port",
			cfg:       ServerConfig{BaseDomain: "tunnel.dev", PublicPort: 8443, TLSEnabled: true},
			subdomain: "app",
			want:      "https://app.tunnel.dev:8443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.TunnelURL(tt.subdomain)
			if got != tt.want {
				t.Errorf("TunnelURL(%q) = %q, want %q", tt.subdomain, got, tt.want)
			}
		})
	}
}

func TestAddrs(t *testing.T) {
	cfg := ServerConfig{PublicPort: 443, AdminPort: 8081, ControlPort: 7000}
	if cfg.PublicAddr() != ":443" {
		t.Errorf("PublicAddr = %q", cfg.PublicAddr())
	}
	if cfg.AdminAddr() != ":8081" {
		t.Errorf("AdminAddr = %q", cfg.AdminAddr())
	}
	if cfg.ControlAddr() != ":7000" {
		t.Errorf("ControlAddr = %q", cfg.ControlAddr())
	}
}
