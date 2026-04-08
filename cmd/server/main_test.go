package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ratatosk/internal/inspector"
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

// setupTunnel creates a yamux tunnel with a CLI-side goroutine that proxies
// requests to the given local server. Returns the registered subdomain.
func setupTunnel(t *testing.T, subdomain string, localAddr string) {
	t.Helper()

	clientPipe, serverPipe := net.Pipe()
	t.Cleanup(func() { clientPipe.Close(); serverPipe.Close() })

	serverSession, err := tunnel.NewServerSession(serverPipe)
	if err != nil {
		t.Fatalf("NewServerSession: %v", err)
	}
	registry.Register(subdomain, serverSession)
	t.Cleanup(func() { registry.Unregister(subdomain) })

	clientSession, err := tunnel.NewClientSession(clientPipe)
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	t.Cleanup(func() { clientSession.Close() })

	logger := inspector.NewLogger()
	go func() {
		for {
			stream, err := clientSession.Accept()
			if err != nil {
				return
			}
			go func() {
				defer stream.Close()
				inspector.HandleStream(stream, localAddr, logger)
			}()
		}
	}()
}

// startProxyServer starts a real HTTP server using handleHTTP so that
// hijacking works (httptest.ResponseRecorder does not support Hijack).
func startProxyServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(handleHTTP)}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func TestHTTPProxySingleRequest(t *testing.T) {
	const want = "<html><body>Hello from local</body></html>"
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, want)
	}))
	defer local.Close()

	setupTunnel(t, "single-req", local.Listener.Addr().String())
	proxyAddr := startProxyServer(t)

	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	fmt.Fprintf(conn, "GET /hello HTTP/1.1\r\nHost: single-req.localhost:8080\r\nConnection: close\r\n\r\n")

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Hello from local") {
		t.Errorf("body = %q, want to contain 'Hello from local'", body)
	}
}

// TestHTTPProxySequentialRequests reproduces the "stays loading" bug:
// after the first request, the hijacked connection is stuck in io.Copy
// and the browser's second request (for JS/CSS) hangs until timeout.
func TestHTTPProxySequentialRequests(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "path=%s", r.URL.Path)
	}))
	defer local.Close()

	setupTunnel(t, "seq-req", local.Listener.Addr().String())
	proxyAddr := startProxyServer(t)

	// Make 3 sequential requests, each on a fresh connection (simulating
	// what a browser does after the dead keep-alive connection is detected).
	for i := range 3 {
		func() {
			conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
			if err != nil {
				t.Fatalf("request %d: Dial: %v", i, err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))

			path := fmt.Sprintf("/resource-%d", i)
			fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: seq-req.localhost:8080\r\nConnection: close\r\n\r\n", path)

			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("request %d: ReadResponse: %v", i, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("request %d: status = %d, want 200", i, resp.StatusCode)
			}
			want := fmt.Sprintf("path=%s", path)
			if string(body) != want {
				t.Errorf("request %d: body = %q, want %q", i, body, want)
			}
		}()
	}
}

// TestHTTPProxyConcurrentRequests simulates a browser loading a page that
// triggers multiple parallel resource fetches (like Swagger UI loading JS/CSS).
func TestHTTPProxyConcurrentRequests(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "ok:%s", r.URL.Path)
	}))
	defer local.Close()

	setupTunnel(t, "conc-req", local.Listener.Addr().String())
	proxyAddr := startProxyServer(t)

	const n = 5
	errs := make(chan error, n)

	for i := range n {
		go func(i int) {
			conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
			if err != nil {
				errs <- fmt.Errorf("request %d: Dial: %w", i, err)
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))

			path := fmt.Sprintf("/asset-%d", i)
			fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: conc-req.localhost:8080\r\nConnection: close\r\n\r\n", path)

			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				errs <- fmt.Errorf("request %d: ReadResponse: %w", i, err)
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			want := fmt.Sprintf("ok:%s", path)
			if resp.StatusCode != http.StatusOK || string(body) != want {
				errs <- fmt.Errorf("request %d: status=%d body=%q, want 200 %q", i, resp.StatusCode, body, want)
				return
			}
			errs <- nil
		}(i)
	}

	for range n {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

// TestHTTPProxyKeepAlive reproduces the "stays loading" bug.
// With HTTP/1.1 keep-alive (no Connection: close), the proxy's handleHTTP
// hijacks the connection and starts bidirectional io.Copy. After the CLI
// writes the response and closes the stream, io.Copy(clientConn, stream)
// finishes but io.Copy(stream, clientConn) blocks forever reading from
// the browser. The clientConn is never closed, so the browser thinks the
// connection is still alive and queues subsequent requests on it — which
// get lost when they're written to the closed stream.
func TestHTTPProxyKeepAlive(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "resp:%s", r.URL.Path)
	}))
	defer local.Close()

	setupTunnel(t, "keepalive", local.Listener.Addr().String())
	proxyAddr := startProxyServer(t)

	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	reader := bufio.NewReader(conn)

	// First request — no Connection: close (keep-alive is default).
	fmt.Fprintf(conn, "GET /page HTTP/1.1\r\nHost: keepalive.localhost:8080\r\n\r\n")
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("request 1: ReadResponse: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "resp:/page" {
		t.Fatalf("request 1: body = %q", body)
	}

	// Second request on the SAME connection (browser keep-alive reuse).
	// This is where the bug manifests: the connection is stuck in io.Copy
	// and this request either gets lost or never receives a response.
	fmt.Fprintf(conn, "GET /script.js HTTP/1.1\r\nHost: keepalive.localhost:8080\r\n\r\n")
	resp2, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("request 2: ReadResponse: %v (connection stuck — keep-alive bug)", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if string(body2) != "resp:/script.js" {
		t.Errorf("request 2: body = %q, want %q", body2, "resp:/script.js")
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
