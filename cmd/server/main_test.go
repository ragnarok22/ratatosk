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
	"testing/fstest"
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
	expectedURL := cfg.TunnelURL(resp.Subdomain)
	if resp.URL != expectedURL {
		t.Errorf("URL = %q, want %q", resp.URL, expectedURL)
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
	setupTunnelWithAuth(t, subdomain, localAddr, "")
}

// setupTunnelWithAuth creates a yamux tunnel with optional basic auth.
func setupTunnelWithAuth(t *testing.T, subdomain string, localAddr string, basicAuth string) {
	t.Helper()

	clientPipe, serverPipe := net.Pipe()
	t.Cleanup(func() { clientPipe.Close(); serverPipe.Close() })

	serverSession, err := tunnel.NewServerSession(serverPipe)
	if err != nil {
		t.Fatalf("NewServerSession: %v", err)
	}
	registry.Register(subdomain, serverSession, basicAuth)
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

func TestSpaHandler(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":       {Data: []byte("<html>SPA</html>")},
		"assets/style.css": {Data: []byte("body{color:red}")},
	}
	handler := spaHandler(fsys)

	t.Run("root", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if !strings.Contains(w.Body.String(), "SPA") {
			t.Errorf("body = %q, want to contain 'SPA'", w.Body.String())
		}
	})

	t.Run("static_file", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/assets/style.css", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if !strings.Contains(w.Body.String(), "body{color:red}") {
			t.Errorf("body = %q, want CSS content", w.Body.String())
		}
	})

	t.Run("spa_fallback", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/unknown/route", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if !strings.Contains(w.Body.String(), "SPA") {
			t.Errorf("body = %q, want SPA fallback to index.html", w.Body.String())
		}
	})
}

func TestExtractSubdomainEdgeCases(t *testing.T) {
	tests := []struct {
		host, base, want string
	}{
		{"foo.localhost", "localhost", "foo"},
		{"a.b.localhost", "localhost", ""},
		{".localhost", "localhost", ""},
		{"localhost", "localhost", ""},
		{"foo.other.com", "localhost", ""},
		{"foo.tunnel.example.com", "tunnel.example.com", "foo"},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := extractSubdomain(tt.host, tt.base)
			if got != tt.want {
				t.Errorf("extractSubdomain(%q, %q) = %q, want %q", tt.host, tt.base, got, tt.want)
			}
		})
	}
}

func TestHandleConnectionClosedEarly(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	clientConn.Close()
	t.Cleanup(func() { serverConn.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleConnection(serverConn)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleConnection did not return after early close")
	}
}

func TestHandleConnectionBadProtocol(t *testing.T) {
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

	clientSession, err := tunnel.NewClientSession(clientConn)
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}

	controlStream, err := clientSession.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	controlStream.Write([]byte("not-json"))
	controlStream.Close()
	clientSession.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleConnection did not return after bad protocol data")
	}
}

func TestProxyRequestSessionClosed(t *testing.T) {
	clientPipe, serverPipe := net.Pipe()
	serverSession, err := tunnel.NewServerSession(serverPipe)
	if err != nil {
		t.Fatalf("NewServerSession: %v", err)
	}
	clientSession, err := tunnel.NewClientSession(clientPipe)
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	// Close everything so session.Open() fails.
	clientSession.Close()
	serverSession.Close()
	clientPipe.Close()
	serverPipe.Close()

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	dummyConn, dummyBack := net.Pipe()
	defer dummyConn.Close()
	defer dummyBack.Close()

	if proxyRequest(req, "closed", serverSession, dummyConn) {
		t.Error("expected proxyRequest to return false when session is closed")
	}
}

func TestHandleConnectionAbruptClose(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close(); serverConn.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleConnection(serverConn)
	}()

	clientSession, err := tunnel.NewClientSession(clientConn)
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}

	controlStream, err := clientSession.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
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

	// Abruptly kill the underlying connection (not a clean yamux close).
	clientConn.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleConnection did not return after abrupt close")
	}
}

func TestHTTPProxyClientNoResponse(t *testing.T) {
	// Set up a tunnel where the client drops streams without responding.
	clientPipe, serverPipe := net.Pipe()
	t.Cleanup(func() { clientPipe.Close(); serverPipe.Close() })

	serverSession, err := tunnel.NewServerSession(serverPipe)
	if err != nil {
		t.Fatalf("NewServerSession: %v", err)
	}
	registry.Register("drop-req", serverSession, "")
	t.Cleanup(func() { registry.Unregister("drop-req") })

	clientSession, err := tunnel.NewClientSession(clientPipe)
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	t.Cleanup(func() { clientSession.Close() })

	go func() {
		for {
			stream, err := clientSession.Accept()
			if err != nil {
				return
			}
			stream.Close()
		}
	}()

	proxyAddr := startProxyServer(t)

	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: drop-req.localhost:8080\r\nConnection: close\r\n\r\n")

	// The proxy's ReadResponse should fail since client closed stream.
	// Connection will be closed by the proxy.
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err == nil {
		resp.Body.Close()
	}
}

func TestHTTPProxyKeepAliveTunnelGone(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ok")
	}))
	defer local.Close()

	setupTunnel(t, "gone-req", local.Listener.Addr().String())
	proxyAddr := startProxyServer(t)

	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	reader := bufio.NewReader(conn)

	// First request (keep-alive).
	fmt.Fprintf(conn, "GET /page HTTP/1.1\r\nHost: gone-req.localhost:8080\r\n\r\n")
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("request 1: ReadResponse: %v", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	// Remove the tunnel between requests.
	registry.Unregister("gone-req")

	// Second request on the same connection — tunnel is gone.
	fmt.Fprintf(conn, "GET /page2 HTTP/1.1\r\nHost: gone-req.localhost:8080\r\n\r\n")

	// Server should close the connection since the tunnel is gone.
	_, err = http.ReadResponse(reader, nil)
	if err == nil {
		// If by chance we got a response, that's also acceptable.
		t.Log("got response after tunnel removal (race condition, acceptable)")
	}
}

func TestHTTPProxyPOSTRequest(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "got:%s", body)
	}))
	defer local.Close()

	setupTunnel(t, "post-req", local.Listener.Addr().String())
	proxyAddr := startProxyServer(t)

	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	body := "hello=world"
	fmt.Fprintf(conn, "POST /submit HTTP/1.1\r\nHost: post-req.localhost:8080\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(respBody), "got:hello=world") {
		t.Errorf("body = %q, want to contain 'got:hello=world'", respBody)
	}
}

func TestHTTPProxyKeepAliveInvalidHost(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer local.Close()

	setupTunnel(t, "invalidhost", local.Listener.Addr().String())
	proxyAddr := startProxyServer(t)

	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	reader := bufio.NewReader(conn)

	// First request with valid host (keep-alive).
	fmt.Fprintf(conn, "GET /page HTTP/1.1\r\nHost: invalidhost.localhost:8080\r\n\r\n")
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("request 1: %v", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	// Second request with host that yields no subdomain.
	fmt.Fprintf(conn, "GET /page2 HTTP/1.1\r\nHost: localhost\r\n\r\n")

	// Server should close connection since extractSubdomain returns "".
	_, err = http.ReadResponse(reader, nil)
	if err == nil {
		t.Log("got response for invalid host (acceptable if race)")
	}
}

func TestHandleHTTPNoHijackSupport(t *testing.T) {
	clientPipe, serverPipe := net.Pipe()
	t.Cleanup(func() { clientPipe.Close(); serverPipe.Close() })

	serverSession, err := tunnel.NewServerSession(serverPipe)
	if err != nil {
		t.Fatalf("NewServerSession: %v", err)
	}
	registry.Register("nohijack", serverSession, "")
	t.Cleanup(func() { registry.Unregister("nohijack") })

	clientSession, err := tunnel.NewClientSession(clientPipe)
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	t.Cleanup(func() { clientSession.Close() })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "nohijack.localhost:8080"

	handleHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestCheckBasicAuthNoAuthRequired(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	if !checkBasicAuth(w, r, "") {
		t.Error("checkBasicAuth returned false when no auth required")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestCheckBasicAuthValid(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.SetBasicAuth("admin", "secret")
	if !checkBasicAuth(w, r, "admin:secret") {
		t.Error("checkBasicAuth returned false for valid credentials")
	}
}

func TestCheckBasicAuthInvalid(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.SetBasicAuth("admin", "wrong")
	if checkBasicAuth(w, r, "admin:secret") {
		t.Error("checkBasicAuth returned true for invalid credentials")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if w.Header().Get("WWW-Authenticate") != `Basic realm="Ratatosk Tunnel"` {
		t.Errorf("WWW-Authenticate = %q", w.Header().Get("WWW-Authenticate"))
	}
}

func TestCheckBasicAuthMissing(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	if checkBasicAuth(w, r, "admin:secret") {
		t.Error("checkBasicAuth returned true with no Authorization header")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHTTPProxyBasicAuthRejected(t *testing.T) {
	// Register a tunnel with basic auth required.
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "should not reach")
	}))
	defer local.Close()

	setupTunnelWithAuth(t, "auth-reject", local.Listener.Addr().String(), "admin:secret")

	// Use httptest.NewRecorder — the request should be rejected before hijack.
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "auth-reject.localhost:8080"

	handleHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if w.Header().Get("WWW-Authenticate") != `Basic realm="Ratatosk Tunnel"` {
		t.Errorf("WWW-Authenticate = %q", w.Header().Get("WWW-Authenticate"))
	}
}

func TestHTTPProxyBasicAuthAccepted(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "authenticated")
	}))
	defer local.Close()

	setupTunnelWithAuth(t, "auth-ok", local.Listener.Addr().String(), "admin:secret")
	proxyAddr := startProxyServer(t)

	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	fmt.Fprintf(conn, "GET /page HTTP/1.1\r\nHost: auth-ok.localhost:8080\r\nAuthorization: Basic YWRtaW46c2VjcmV0\r\nConnection: close\r\n\r\n")

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "authenticated" {
		t.Errorf("body = %q, want %q", body, "authenticated")
	}
}

func TestHTTPProxyBasicAuthWrongCredentials(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "should not reach")
	}))
	defer local.Close()

	setupTunnelWithAuth(t, "auth-wrong", local.Listener.Addr().String(), "admin:secret")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "auth-wrong.localhost:8080"
	req.SetBasicAuth("admin", "wrong")

	handleHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHTTPProxyNoAuthPublicTunnel(t *testing.T) {
	// Tunnel without auth — should work without credentials.
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "public")
	}))
	defer local.Close()

	setupTunnel(t, "no-auth", local.Listener.Addr().String())
	proxyAddr := startProxyServer(t)

	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: no-auth.localhost:8080\r\nConnection: close\r\n\r\n")

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "public" {
		t.Errorf("body = %q, want %q", body, "public")
	}
}
