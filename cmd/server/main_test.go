package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ratatosk/internal/protocol"
	"ratatosk/internal/tunnel"
)

func TestHandleConnectionHandshake(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleConnection(serverConn)
	}()

	// Client side: create yamux session and perform handshake.
	clientSession, err := tunnel.NewClientSession(clientConn)
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}

	controlStream, err := clientSession.Open()
	if err != nil {
		t.Fatalf("Open control stream: %v", err)
	}

	req := &protocol.TunnelRequest{Protocol: "http", LocalPort: 3000}
	if err := protocol.WriteRequest(controlStream, req); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	resp, err := protocol.ReadResponse(controlStream)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	controlStream.Close()

	if !resp.Success {
		t.Fatalf("handshake failed: %s", resp.Error)
	}
	if resp.Subdomain == "" {
		t.Fatal("empty subdomain in response")
	}

	// Verify the subdomain was registered.
	if !registry.HasSubdomain(resp.Subdomain) {
		t.Fatalf("subdomain %q not found in registry", resp.Subdomain)
	}

	// Close the client session and wait for the server to unregister.
	clientSession.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleConnection did not return after client disconnect")
	}

	if registry.HasSubdomain(resp.Subdomain) {
		t.Fatalf("subdomain %q still in registry after disconnect", resp.Subdomain)
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

func TestAdminAPITunnels(t *testing.T) {
	handler := newAdminHandler(registry)
	req := httptest.NewRequest(http.MethodGet, "/api/tunnels", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}

	var body struct {
		Tunnels []tunnel.TunnelInfo `json:"tunnels"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
}

func TestAdminDashboardFallback(t *testing.T) {
	handler := newAdminHandler(registry)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}
