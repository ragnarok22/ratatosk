package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearConfigEnv(t *testing.T) {
	t.Helper()

	for _, key := range []string{
		"RATATOSK_BASE_DOMAIN",
		"RATATOSK_PUBLIC_PORT",
		"RATATOSK_ADMIN_PORT",
		"RATATOSK_CONTROL_PORT",
		"RATATOSK_TLS_ENABLED",
		"RATATOSK_PORT_RANGE_START",
		"RATATOSK_PORT_RANGE_END",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	// Ensure no config file or env vars interfere.
	clearConfigEnv(t)

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
	if cfg.PortRangeStart != 10000 {
		t.Errorf("PortRangeStart = %d, want %d", cfg.PortRangeStart, 10000)
	}
	if cfg.PortRangeEnd != 20000 {
		t.Errorf("PortRangeEnd = %d, want %d", cfg.PortRangeEnd, 20000)
	}
}

func TestLoadConfigPortRangeFromEnv(t *testing.T) {
	t.Setenv("RATATOSK_PORT_RANGE_START", "5000")
	t.Setenv("RATATOSK_PORT_RANGE_END", "6000")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.PortRangeStart != 5000 {
		t.Errorf("PortRangeStart = %d, want %d", cfg.PortRangeStart, 5000)
	}
	if cfg.PortRangeEnd != 6000 {
		t.Errorf("PortRangeEnd = %d, want %d", cfg.PortRangeEnd, 6000)
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

func TestLoadConfigSearchPathAndPrecedence(t *testing.T) {
	tests := []struct {
		name            string
		fileContent     string
		env             map[string]string
		wantBaseDomain  string
		wantPublicPort  int
		wantAdminPort   int
		wantControlPort int
		wantTLSEnabled  bool
	}{
		{
			name: "loads from home directory config path",
			fileContent: `base_domain: home.tunnel.dev
public_port: 9443
admin_port: 9092
control_port: 7100
tls_enabled: true
`,
			wantBaseDomain:  "home.tunnel.dev",
			wantPublicPort:  9443,
			wantAdminPort:   9092,
			wantControlPort: 7100,
			wantTLSEnabled:  true,
		},
		{
			name: "environment variables override file values",
			fileContent: `base_domain: file.tunnel.dev
public_port: 8088
admin_port: 8089
control_port: 7099
tls_enabled: false
`,
			env: map[string]string{
				"RATATOSK_BASE_DOMAIN": "env.tunnel.dev",
				"RATATOSK_PUBLIC_PORT": "443",
				"RATATOSK_TLS_ENABLED": "true",
			},
			wantBaseDomain:  "env.tunnel.dev",
			wantPublicPort:  443,
			wantAdminPort:   8089,
			wantControlPort: 7099,
			wantTLSEnabled:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			homeDir := t.TempDir()
			configDir := filepath.Join(homeDir, ".ratatosk")
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				t.Fatalf("Mkdir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(configDir, "ratatosk.yaml"), []byte(tt.fileContent), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			// Move to an unrelated directory to prove the home-directory search path is used.
			orig, _ := os.Getwd()
			emptyDir := t.TempDir()
			if err := os.Chdir(emptyDir); err != nil {
				t.Fatalf("Chdir: %v", err)
			}
			t.Cleanup(func() { _ = os.Chdir(orig) })

			t.Setenv("HOME", homeDir)
			clearConfigEnv(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}

			if cfg.BaseDomain != tt.wantBaseDomain {
				t.Errorf("BaseDomain = %q, want %q", cfg.BaseDomain, tt.wantBaseDomain)
			}
			if cfg.PublicPort != tt.wantPublicPort {
				t.Errorf("PublicPort = %d, want %d", cfg.PublicPort, tt.wantPublicPort)
			}
			if cfg.AdminPort != tt.wantAdminPort {
				t.Errorf("AdminPort = %d, want %d", cfg.AdminPort, tt.wantAdminPort)
			}
			if cfg.ControlPort != tt.wantControlPort {
				t.Errorf("ControlPort = %d, want %d", cfg.ControlPort, tt.wantControlPort)
			}
			if cfg.TLSEnabled != tt.wantTLSEnabled {
				t.Errorf("TLSEnabled = %t, want %t", cfg.TLSEnabled, tt.wantTLSEnabled)
			}
		})
	}
}

func TestLoadConfigFromHomeConfigDir(t *testing.T) {
	homeDir := t.TempDir()
	configDir := filepath.Join(homeDir, ".ratatosk")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := []byte(`base_domain: home.example
public_port: 18080
`)
	if err := os.WriteFile(filepath.Join(configDir, "ratatosk.yaml"), content, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("HOME", homeDir)

	// Ensure current directory does not contain a ratatosk.yaml file.
	cwd := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.BaseDomain != "home.example" {
		t.Errorf("BaseDomain = %q, want %q", cfg.BaseDomain, "home.example")
	}
	if cfg.PublicPort != 18080 {
		t.Errorf("PublicPort = %d, want %d", cfg.PublicPort, 18080)
	}
}

func TestLoadConfigMissingHomeDir(t *testing.T) {
	orig := osUserHomeDir
	osUserHomeDir = func() (string, error) {
		return "", errors.New("home unavailable")
	}
	t.Cleanup(func() {
		osUserHomeDir = orig
	})

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error when home directory cannot be resolved")
	}
	if !strings.Contains(err.Error(), "resolving home directory") {
		t.Fatalf("error = %q, want context about resolving home directory", err.Error())
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
