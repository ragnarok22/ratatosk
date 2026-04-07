package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateSubdomain(t *testing.T) {
	sub, err := generateSubdomain()
	if err != nil {
		t.Fatalf("generateSubdomain: %v", err)
	}
	if len(sub) != 6 {
		t.Errorf("subdomain length = %d, want 6 (hex of 3 bytes)", len(sub))
	}
}

func TestGenerateSubdomainUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		sub, err := generateSubdomain()
		if err != nil {
			t.Fatalf("generateSubdomain: %v", err)
		}
		if seen[sub] {
			t.Fatalf("duplicate subdomain: %s", sub)
		}
		seen[sub] = true
	}
}

func TestHandleHTTPInvalidHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "localhost:8080" // No subdomain dot-separator.
	w := httptest.NewRecorder()

	handleHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleHTTPTunnelNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "unknown.localhost:8080"
	w := httptest.NewRecorder()

	handleHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
}
