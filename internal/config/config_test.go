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
		"RATATOSK_ADMIN_HOST",
		"RATATOSK_ADMIN_PORT",
		"RATATOSK_ADMIN_USERNAME",
		"RATATOSK_ADMIN_PASSWORD",
		"RATATOSK_ADMIN_TLS_ENABLED",
		"RATATOSK_ADMIN_TLS_CERT_FILE",
		"RATATOSK_ADMIN_TLS_KEY_FILE",
		"RATATOSK_CONTROL_HOST",
		"RATATOSK_CONTROL_PORT",
		"RATATOSK_CONTROL_TOKEN",
		"RATATOSK_CONTROL_TLS_ENABLED",
		"RATATOSK_CONTROL_TLS_CERT_FILE",
		"RATATOSK_CONTROL_TLS_KEY_FILE",
		"RATATOSK_TLS_ENABLED",
		"RATATOSK_TLS_AUTO",
		"RATATOSK_TLS_EMAIL",
		"RATATOSK_TLS_PROVIDER",
		"RATATOSK_TLS_API_TOKEN",
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
	if cfg.AdminHost != "127.0.0.1" {
		t.Errorf("AdminHost = %q, want %q", cfg.AdminHost, "127.0.0.1")
	}
	if cfg.AdminPort != 8081 {
		t.Errorf("AdminPort = %d, want %d", cfg.AdminPort, 8081)
	}
	if cfg.ControlHost != "127.0.0.1" {
		t.Errorf("ControlHost = %q, want %q", cfg.ControlHost, "127.0.0.1")
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
	t.Setenv("RATATOSK_ADMIN_HOST", "10.0.0.2")
	t.Setenv("RATATOSK_ADMIN_USERNAME", "admin")
	t.Setenv("RATATOSK_ADMIN_PASSWORD", "secret")
	t.Setenv("RATATOSK_ADMIN_TLS_ENABLED", "true")
	t.Setenv("RATATOSK_ADMIN_TLS_CERT_FILE", "/etc/ssl/admin-cert.pem")
	t.Setenv("RATATOSK_ADMIN_TLS_KEY_FILE", "/etc/ssl/admin-key.pem")
	t.Setenv("RATATOSK_CONTROL_HOST", "0.0.0.0")
	t.Setenv("RATATOSK_CONTROL_TOKEN", "control-secret")
	t.Setenv("RATATOSK_CONTROL_TLS_ENABLED", "true")
	t.Setenv("RATATOSK_CONTROL_TLS_CERT_FILE", "/etc/ssl/control-cert.pem")
	t.Setenv("RATATOSK_CONTROL_TLS_KEY_FILE", "/etc/ssl/control-key.pem")
	t.Setenv("RATATOSK_TLS_ENABLED", "true")
	t.Setenv("RATATOSK_TLS_CERT_FILE", "/etc/ssl/cert.pem")
	t.Setenv("RATATOSK_TLS_KEY_FILE", "/etc/ssl/key.pem")

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
	if cfg.AdminHost != "10.0.0.2" {
		t.Errorf("AdminHost = %q, want %q", cfg.AdminHost, "10.0.0.2")
	}
	if cfg.ControlHost != "0.0.0.0" {
		t.Errorf("ControlHost = %q, want %q", cfg.ControlHost, "0.0.0.0")
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
tls_cert_file: /etc/ssl/cert.pem
tls_key_file: /etc/ssl/key.pem
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
				"RATATOSK_BASE_DOMAIN":   "env.tunnel.dev",
				"RATATOSK_PUBLIC_PORT":   "443",
				"RATATOSK_TLS_ENABLED":   "true",
				"RATATOSK_TLS_CERT_FILE": "/etc/ssl/cert.pem",
				"RATATOSK_TLS_KEY_FILE":  "/etc/ssl/key.pem",
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
port_range_start: 20000
port_range_end: 30000
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
		{
			name:      "auto TLS on port 443",
			cfg:       ServerConfig{BaseDomain: "tunnel.dev", PublicPort: 443, TLSAuto: true},
			subdomain: "app",
			want:      "https://app.tunnel.dev",
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
	cfg := ServerConfig{
		PublicPort:  443,
		AdminHost:   "127.0.0.1",
		AdminPort:   8081,
		ControlHost: "::1",
		ControlPort: 7000,
	}
	if cfg.PublicAddr() != ":443" {
		t.Errorf("PublicAddr = %q", cfg.PublicAddr())
	}
	if cfg.AdminAddr() != "127.0.0.1:8081" {
		t.Errorf("AdminAddr = %q", cfg.AdminAddr())
	}
	if cfg.ControlAddr() != "[::1]:7000" {
		t.Errorf("ControlAddr = %q", cfg.ControlAddr())
	}
}

func validServerConfig() ServerConfig {
	return ServerConfig{
		BaseDomain:     "localhost",
		PublicPort:     8080,
		AdminHost:      "127.0.0.1",
		AdminPort:      8081,
		ControlHost:    "127.0.0.1",
		ControlPort:    7000,
		PortRangeStart: 10000,
		PortRangeEnd:   20000,
	}
}

func TestValidateHTTPPassesWithDefaults(t *testing.T) {
	cfg := validServerConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateTLSAutoAndEnabledMutuallyExclusive(t *testing.T) {
	cfg := validServerConfig()
	cfg.TLSAuto = true
	cfg.TLSEnabled = true
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when both tls_auto and tls_enabled are true")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %q, want mention of mutually exclusive", err.Error())
	}
}

func TestValidateTLSAutoRequiresEmail(t *testing.T) {
	cfg := validServerConfig()
	cfg.BaseDomain = "tunnel.example.com"
	cfg.PublicPort = 443
	cfg.TLSAuto = true
	cfg.TLSProvider = "cloudflare"
	cfg.TLSAPIToken = "token"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when tls_email is missing")
	}
	if !strings.Contains(err.Error(), "tls_email") {
		t.Fatalf("error = %q, want mention of tls_email", err.Error())
	}
}

func TestValidateTLSAutoRequiresProvider(t *testing.T) {
	cfg := validServerConfig()
	cfg.BaseDomain = "tunnel.example.com"
	cfg.PublicPort = 443
	cfg.TLSAuto = true
	cfg.TLSEmail = "admin@example.com"
	cfg.TLSAPIToken = "token"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when tls_provider is missing")
	}
	if !strings.Contains(err.Error(), "tls_provider") {
		t.Fatalf("error = %q, want mention of tls_provider", err.Error())
	}
}

func TestValidateTLSAutoRequiresAPIToken(t *testing.T) {
	cfg := validServerConfig()
	cfg.BaseDomain = "tunnel.example.com"
	cfg.PublicPort = 443
	cfg.TLSAuto = true
	cfg.TLSEmail = "admin@example.com"
	cfg.TLSProvider = "cloudflare"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when tls_api_token is missing")
	}
	if !strings.Contains(err.Error(), "tls_api_token") {
		t.Fatalf("error = %q, want mention of tls_api_token", err.Error())
	}
}

func TestValidateTLSAutoRejectsLocalhost(t *testing.T) {
	cfg := validServerConfig()
	cfg.PublicPort = 443
	cfg.TLSAuto = true
	cfg.TLSEmail = "admin@example.com"
	cfg.TLSProvider = "cloudflare"
	cfg.TLSAPIToken = "token"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when base_domain is localhost with tls_auto")
	}
	if !strings.Contains(err.Error(), "localhost") {
		t.Fatalf("error = %q, want mention of localhost", err.Error())
	}
}

func TestValidateManualTLSRequiresCertAndKey(t *testing.T) {
	cfg := validServerConfig()
	cfg.TLSEnabled = true
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when tls_cert_file and tls_key_file are missing")
	}
	if !strings.Contains(err.Error(), "tls_cert_file") {
		t.Fatalf("error = %q, want mention of tls_cert_file", err.Error())
	}
}

func TestValidateTLSAutoValid(t *testing.T) {
	cfg := validServerConfig()
	cfg.BaseDomain = "tunnel.example.com"
	cfg.PublicPort = 443
	cfg.TLSAuto = true
	cfg.TLSEmail = "admin@example.com"
	cfg.TLSProvider = "cloudflare"
	cfg.TLSAPIToken = "token"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateManualTLSValid(t *testing.T) {
	cfg := validServerConfig()
	cfg.TLSEnabled = true
	cfg.TLSCertFile = "/etc/ssl/cert.pem"
	cfg.TLSKeyFile = "/etc/ssl/key.pem"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsInvalidPortsAndRanges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ServerConfig)
	}{
		{name: "negative public port", mutate: func(cfg *ServerConfig) { cfg.PublicPort = -1 }},
		{name: "admin port above maximum", mutate: func(cfg *ServerConfig) { cfg.AdminPort = 70000 }},
		{name: "zero control port", mutate: func(cfg *ServerConfig) { cfg.ControlPort = 0 }},
		{name: "range is reversed", mutate: func(cfg *ServerConfig) { cfg.PortRangeStart = 20000; cfg.PortRangeEnd = 10000 }},
		{name: "range starts below minimum", mutate: func(cfg *ServerConfig) { cfg.PortRangeStart = 0 }},
		{name: "range ends above maximum", mutate: func(cfg *ServerConfig) { cfg.PortRangeEnd = 65536 }},
		{name: "service ports conflict", mutate: func(cfg *ServerConfig) { cfg.AdminPort = cfg.PublicPort }},
		{name: "public port overlaps range", mutate: func(cfg *ServerConfig) { cfg.PublicPort = 10000 }},
		{name: "admin port overlaps range", mutate: func(cfg *ServerConfig) { cfg.AdminPort = 15000 }},
		{name: "control port overlaps range", mutate: func(cfg *ServerConfig) { cfg.ControlPort = 19999 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validServerConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Errorf("Validate accepted invalid configuration: %+v", cfg)
			}
		})
	}
}

func TestValidateTLSAutoRequiresPort443(t *testing.T) {
	cfg := validServerConfig()
	cfg.BaseDomain = "tunnel.example.com"
	cfg.TLSAuto = true
	cfg.TLSEmail = "admin@example.com"
	cfg.TLSProvider = "cloudflare"
	cfg.TLSAPIToken = "token"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted tls_auto with a public port other than 443")
	}
}

func TestValidateRejectsUnprotectedRemoteBindings(t *testing.T) {
	base := ServerConfig{
		BaseDomain:     "example.com",
		PublicPort:     8080,
		AdminHost:      "127.0.0.1",
		AdminPort:      8081,
		ControlHost:    "127.0.0.1",
		ControlPort:    7000,
		PortRangeStart: 10000,
		PortRangeEnd:   20000,
	}

	remoteAdmin := base
	remoteAdmin.AdminHost = "0.0.0.0"
	if err := remoteAdmin.Validate(); err == nil {
		t.Fatal("Validate accepted an unauthenticated remote admin binding")
	}

	remoteControl := base
	remoteControl.ControlHost = "0.0.0.0"
	if err := remoteControl.Validate(); err == nil {
		t.Fatal("Validate accepted an unauthenticated plaintext remote control binding")
	}
}

func TestValidateAcceptsProtectedRemoteBindings(t *testing.T) {
	cfg := validServerConfig()
	cfg.AdminHost = "0.0.0.0"
	cfg.AdminUsername = "admin"
	cfg.AdminPassword = "secret"
	cfg.AdminTLSEnabled = true
	cfg.AdminTLSCertFile = "/etc/ssl/admin-cert.pem"
	cfg.AdminTLSKeyFile = "/etc/ssl/admin-key.pem"
	cfg.ControlHost = "0.0.0.0"
	cfg.ControlToken = "control-secret"
	cfg.ControlTLSEnabled = true
	cfg.ControlTLSCertFile = "/etc/ssl/control-cert.pem"
	cfg.ControlTLSKeyFile = "/etc/ssl/control-key.pem"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateAdminTLSRequiresCertAndKey(t *testing.T) {
	cfg := validServerConfig()
	cfg.AdminTLSEnabled = true

	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "admin_tls_cert_file") {
		t.Fatalf("Validate error = %v, want admin TLS certificate requirement", err)
	}
}

func TestValidateControlTLSRequiresCertAndKey(t *testing.T) {
	cfg := validServerConfig()
	cfg.ControlTLSEnabled = true

	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "control_tls_cert_file") {
		t.Fatalf("Validate error = %v, want control TLS certificate requirement", err)
	}
}

func TestValidateRejectsInvalidDomainAndTLSProvider(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ServerConfig)
	}{
		{name: "empty domain", mutate: func(cfg *ServerConfig) { cfg.BaseDomain = "" }},
		{name: "URL is not a domain", mutate: func(cfg *ServerConfig) { cfg.BaseDomain = "https://example.com" }},
		{name: "empty label", mutate: func(cfg *ServerConfig) { cfg.BaseDomain = "tunnel..example.com" }},
		{name: "label starts with hyphen", mutate: func(cfg *ServerConfig) { cfg.BaseDomain = "-tunnel.example.com" }},
		{name: "IP address", mutate: func(cfg *ServerConfig) { cfg.BaseDomain = "127.0.0.1" }},
		{name: "unsupported TLS provider", mutate: func(cfg *ServerConfig) {
			cfg.BaseDomain = "tunnel.example.com"
			cfg.PublicPort = 443
			cfg.TLSAuto = true
			cfg.TLSEmail = "admin@example.com"
			cfg.TLSProvider = "route53"
			cfg.TLSAPIToken = "token"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validServerConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Errorf("Validate accepted invalid configuration: %+v", cfg)
			}
		})
	}
}
