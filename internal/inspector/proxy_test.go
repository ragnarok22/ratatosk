package inspector

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleStream(t *testing.T) {
	// Start a local HTTP server to act as the user's app.
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "hello")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "response-body")
	}))
	defer local.Close()

	// local.URL is "http://127.0.0.1:PORT" — extract host:port.
	localAddr := strings.TrimPrefix(local.URL, "http://")

	logger := NewLogger()

	// Create an in-memory connection pair to simulate a yamux stream.
	clientConn, serverConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		HandleStream(serverConn, localAddr, logger)
		serverConn.Close()
	}()

	// Write a raw HTTP request into the pipe.
	rawReq := "GET /test-path HTTP/1.1\r\nHost: example.com\r\nContent-Length: 0\r\n\r\n"
	_, err := clientConn.Write([]byte(rawReq))
	if err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	// Read the HTTP response from the pipe.
	resp, err := http.ReadResponse(bufio.NewReader(clientConn), nil)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	defer resp.Body.Close()
	clientConn.Close()

	<-done

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	entries := logger.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(entries))
	}

	e := entries[0]
	if e.Method != "GET" {
		t.Errorf("expected method GET, got %s", e.Method)
	}
	if e.Path != "/test-path" {
		t.Errorf("expected path /test-path, got %s", e.Path)
	}
	if e.RespStatus != 200 {
		t.Errorf("expected resp status 200, got %d", e.RespStatus)
	}
	if e.RespBody != "response-body" {
		t.Errorf("expected resp body 'response-body', got %q", e.RespBody)
	}
	if e.Duration <= 0 {
		t.Error("expected positive duration")
	}
}

func TestHandleStreamLocalServerDown(t *testing.T) {
	logger := NewLogger()

	clientConn, serverConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Use an address with no server listening.
		HandleStream(serverConn, "127.0.0.1:1", logger)
		serverConn.Close()
	}()

	rawReq := "GET / HTTP/1.1\r\nHost: example.com\r\nContent-Length: 0\r\n\r\n"
	clientConn.Write([]byte(rawReq))

	resp, err := http.ReadResponse(bufio.NewReader(clientConn), nil)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	defer resp.Body.Close()
	clientConn.Close()

	<-done

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", resp.StatusCode)
	}
}

func TestFlattenHeaders(t *testing.T) {
	h := http.Header{
		"Content-Type": {"application/json"},
		"Accept":       {"text/html", "application/json"},
	}

	flat := flattenHeaders(h)
	if flat["Content-Type"] != "application/json" {
		t.Errorf("unexpected Content-Type: %q", flat["Content-Type"])
	}
	if flat["Accept"] != "text/html, application/json" {
		t.Errorf("unexpected Accept: %q", flat["Accept"])
	}
}
