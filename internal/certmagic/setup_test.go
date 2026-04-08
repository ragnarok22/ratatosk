package certmagic

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/caddyserver/certmagic"
)

func TestNewDNSProviderCloudflare(t *testing.T) {
	p, err := NewDNSProvider("cloudflare", "test-token")
	if err != nil {
		t.Fatalf("NewDNSProvider: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestNewDNSProviderUnsupported(t *testing.T) {
	_, err := NewDNSProvider("route53", "test-token")
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
	if !strings.Contains(err.Error(), "unsupported DNS provider") {
		t.Fatalf("error = %q, want mention of unsupported provider", err.Error())
	}
}

func TestSetupAndServeConfiguresACMEAndCallsHTTPS(t *testing.T) {
	oldHTTPS := httpsFunc
	oldEmail := certmagic.DefaultACME.Email
	oldAgreed := certmagic.DefaultACME.Agreed
	oldSolver := certmagic.DefaultACME.DNS01Solver
	t.Cleanup(func() {
		httpsFunc = oldHTTPS
		certmagic.DefaultACME.Email = oldEmail
		certmagic.DefaultACME.Agreed = oldAgreed
		certmagic.DefaultACME.DNS01Solver = oldSolver
	})

	var gotDomains []string
	var gotHandler http.Handler

	httpsFunc = func(domains []string, handler http.Handler) error {
		gotDomains = domains
		gotHandler = handler
		return nil
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	cfg := Config{
		Email:    "test@example.com",
		Provider: "cloudflare",
		APIToken: "cf-token",
		Domains:  []string{"example.com", "*.example.com"},
	}

	err := SetupAndServe(cfg, handler)
	if err != nil {
		t.Fatalf("SetupAndServe: %v", err)
	}

	if certmagic.DefaultACME.Email != "test@example.com" {
		t.Errorf("ACME email = %q, want %q", certmagic.DefaultACME.Email, "test@example.com")
	}
	if !certmagic.DefaultACME.Agreed {
		t.Error("ACME Agreed should be true")
	}
	if certmagic.DefaultACME.DNS01Solver == nil {
		t.Fatal("ACME DNS01Solver should not be nil")
	}
	if len(gotDomains) != 2 || gotDomains[0] != "example.com" || gotDomains[1] != "*.example.com" {
		t.Errorf("domains = %v, want [example.com *.example.com]", gotDomains)
	}
	if gotHandler == nil {
		t.Error("handler should not be nil")
	}
}

func TestSetupAndServeReturnsHTTPSError(t *testing.T) {
	oldHTTPS := httpsFunc
	oldSolver := certmagic.DefaultACME.DNS01Solver
	t.Cleanup(func() {
		httpsFunc = oldHTTPS
		certmagic.DefaultACME.DNS01Solver = oldSolver
	})

	wantErr := errors.New("https bind failed")
	httpsFunc = func([]string, http.Handler) error {
		return wantErr
	}

	err := SetupAndServe(Config{
		Email:    "test@example.com",
		Provider: "cloudflare",
		APIToken: "token",
		Domains:  []string{"example.com"},
	}, nil)

	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestSetupAndServeReturnsProviderError(t *testing.T) {
	err := SetupAndServe(Config{
		Email:    "test@example.com",
		Provider: "unsupported",
		APIToken: "token",
		Domains:  []string{"example.com"},
	}, nil)

	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
	if !strings.Contains(err.Error(), "unsupported DNS provider") {
		t.Fatalf("err = %q, want mention of unsupported provider", err.Error())
	}
}
