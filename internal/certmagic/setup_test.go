package certmagic

import (
	"strings"
	"testing"
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
