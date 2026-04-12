package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	autocert "ratatosk/internal/certmagic"
	"ratatosk/internal/config"
	"ratatosk/internal/inspector"
	"ratatosk/internal/protocol"
	"ratatosk/internal/tunnel"
)

var noopCheckUpdate = func(string) string { return "" }

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (failingReadCloser) Close() error {
	return nil
}

type stubAddr string

func (a stubAddr) Network() string {
	return "tcp"
}

func (a stubAddr) String() string {
	return string(a)
}

type stubListener struct {
	closed chan struct{}
}

func newStubListener() *stubListener {
	return &stubListener{closed: make(chan struct{})}
}

func (l *stubListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *stubListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *stubListener) Addr() net.Addr {
	return stubAddr("127.0.0.1:0")
}

type errorThenCloseListener struct {
	closed chan struct{}
	err    error
	mu     sync.Mutex
	sent   bool
}

func newErrorThenCloseListener(err error) *errorThenCloseListener {
	return &errorThenCloseListener{
		closed: make(chan struct{}),
		err:    err,
	}
}

func (l *errorThenCloseListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if !l.sent {
		l.sent = true
		l.mu.Unlock()
		return nil, l.err
	}
	l.mu.Unlock()

	<-l.closed
	return nil, net.ErrClosed
}

func (l *errorThenCloseListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *errorThenCloseListener) Addr() net.Addr {
	return stubAddr("127.0.0.1:0")
}

func newLoopbackServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	server := httptest.NewUnstartedServer(handler)
	server.Listener = ln
	server.Start()
	t.Cleanup(server.Close)
	return server
}

type brokenFS struct{}

func (brokenFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

func (brokenFS) Sub(string) (fs.FS, error) {
	return nil, fs.ErrNotExist
}

func TestLoadServerConfig(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })

	want := &config.ServerConfig{
		BaseDomain:  "example.test",
		PublicPort:  8443,
		AdminPort:   9001,
		ControlPort: 7001,
		TLSEnabled:  true,
	}

	if err := loadServerConfig(func() (*config.ServerConfig, error) {
		return want, nil
	}); err != nil {
		t.Fatalf("loadServerConfig: %v", err)
	}

	if cfg != want {
		t.Fatalf("cfg = %#v, want %#v", cfg, want)
	}
}

func TestMainExitsOnLoadConfigError(t *testing.T) {
	oldStdout := mainStdout
	oldExit := mainExit
	oldLoadConfig := mainLoadConfig
	oldListen := mainListen
	oldServe := mainListenAndServe
	oldServeTLS := mainListenAndServeTLS
	t.Cleanup(func() {
		mainStdout = oldStdout
		mainExit = oldExit
		mainLoadConfig = oldLoadConfig
		mainListen = oldListen
		mainListenAndServe = oldServe
		mainListenAndServeTLS = oldServeTLS
	})

	var stdout bytes.Buffer
	exitCode := -1

	mainStdout = &stdout
	mainLoadConfig = func() (*config.ServerConfig, error) {
		return nil, errors.New("boom")
	}
	mainListen = func(network, address string) (net.Listener, error) {
		t.Fatal("mainListen should not be called")
		return nil, nil
	}
	mainListenAndServe = func(addr string, handler http.Handler) error {
		t.Fatal("mainListenAndServe should not be called")
		return nil
	}
	mainListenAndServeTLS = func(addr, certFile, keyFile string, handler http.Handler) error {
		t.Fatal("mainListenAndServeTLS should not be called")
		return nil
	}
	mainExit = func(code int) {
		exitCode = code
	}

	main()

	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(stdout.String(), "failed to load config") {
		t.Fatalf("stdout = %q, want load config error", stdout.String())
	}
}

func TestLoadServerConfigError(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })

	wantErr := errors.New("bad config")
	err := loadServerConfig(func() (*config.ServerConfig, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestStartControlPlaneListenError(t *testing.T) {
	oldCfg := cfg
	cfg = &config.ServerConfig{ControlPort: 7000}
	t.Cleanup(func() { cfg = oldCfg })

	stop := make(chan struct{})
	wantErr := errors.New("listen failed")

	err := startControlPlane(stop, func(network, address string) (net.Listener, error) {
		if network != "tcp" {
			t.Fatalf("network = %q, want tcp", network)
		}
		if address != cfg.ControlAddr() {
			t.Fatalf("address = %q, want %q", address, cfg.ControlAddr())
		}
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestStartControlPlane(t *testing.T) {
	oldCfg := cfg
	cfg = &config.ServerConfig{ControlPort: 0}
	t.Cleanup(func() { cfg = oldCfg })

	stop := make(chan struct{})
	listenCalled := make(chan struct{})
	listener := newStubListener()

	err := startControlPlane(stop, func(network, address string) (net.Listener, error) {
		close(listenCalled)
		return listener, nil
	})
	if err != nil {
		t.Fatalf("startControlPlane: %v", err)
	}

	select {
	case <-listenCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("listen function was not called")
	}

	close(stop)
}

func TestStartControlPlaneAcceptError(t *testing.T) {
	oldCfg := cfg
	cfg = &config.ServerConfig{ControlPort: 7000}
	t.Cleanup(func() { cfg = oldCfg })

	stop := make(chan struct{})
	listener := newErrorThenCloseListener(errors.New("accept failed"))

	if err := startControlPlane(stop, func(network, address string) (net.Listener, error) {
		return listener, nil
	}); err != nil {
		t.Fatalf("startControlPlane: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		listener.mu.Lock()
		sent := listener.sent
		listener.mu.Unlock()
		if sent {
			close(stop)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	close(stop)
	t.Fatal("accept loop did not observe listener error")
}

func TestRunMainStartControlPlaneError(t *testing.T) {
	oldCfg := cfg
	oldStartControlPlane := serverStartControlPlane
	oldStartAdminServer := serverStartAdminServer
	oldStartPublicServer := serverStartPublicServer
	t.Cleanup(func() {
		cfg = oldCfg
		serverStartControlPlane = oldStartControlPlane
		serverStartAdminServer = oldStartAdminServer
		serverStartPublicServer = oldStartPublicServer
	})

	wantErr := errors.New("control plane failed")
	serverStartControlPlane = func(stop <-chan struct{}, listen func(network, address string) (net.Listener, error)) error {
		return wantErr
	}
	serverStartAdminServer = func(stop <-chan struct{}, serve func(addr string, handler http.Handler) error) <-chan error {
		t.Fatal("serverStartAdminServer should not be called")
		return nil
	}
	serverStartPublicServer = func(stop <-chan struct{}, serve func(addr string, handler http.Handler) error, serveTLS func(addr, certFile, keyFile string, handler http.Handler) error) <-chan error {
		t.Fatal("serverStartPublicServer should not be called")
		return nil
	}

	code := runMain(io.Discard, func() (*config.ServerConfig, error) {
		return &config.ServerConfig{
			BaseDomain:     "localhost",
			PublicPort:     8080,
			AdminPort:      8081,
			ControlPort:    7000,
			PortRangeStart: 33000,
			PortRangeEnd:   33010,
		}, nil
	}, nil, nil, nil)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}

func TestRunMainAdminServerError(t *testing.T) {
	oldCfg := cfg
	oldStartControlPlane := serverStartControlPlane
	oldStartAdminServer := serverStartAdminServer
	oldStartPublicServer := serverStartPublicServer
	t.Cleanup(func() {
		cfg = oldCfg
		serverStartControlPlane = oldStartControlPlane
		serverStartAdminServer = oldStartAdminServer
		serverStartPublicServer = oldStartPublicServer
	})

	serverStartControlPlane = func(stop <-chan struct{}, listen func(network, address string) (net.Listener, error)) error {
		return nil
	}
	serverStartAdminServer = func(stop <-chan struct{}, serve func(addr string, handler http.Handler) error) <-chan error {
		errs := make(chan error, 1)
		errs <- errors.New("admin failed")
		return errs
	}
	serverStartPublicServer = func(stop <-chan struct{}, serve func(addr string, handler http.Handler) error, serveTLS func(addr, certFile, keyFile string, handler http.Handler) error) <-chan error {
		return make(chan error)
	}

	code := runMain(io.Discard, func() (*config.ServerConfig, error) {
		return &config.ServerConfig{
			BaseDomain:     "localhost",
			PublicPort:     8080,
			AdminPort:      8081,
			ControlPort:    7000,
			PortRangeStart: 33000,
			PortRangeEnd:   33010,
		}, nil
	}, nil, nil, nil)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}

func TestRunMainPublicHTTPServerError(t *testing.T) {
	oldCfg := cfg
	oldStartControlPlane := serverStartControlPlane
	oldStartAdminServer := serverStartAdminServer
	oldStartPublicServer := serverStartPublicServer
	t.Cleanup(func() {
		cfg = oldCfg
		serverStartControlPlane = oldStartControlPlane
		serverStartAdminServer = oldStartAdminServer
		serverStartPublicServer = oldStartPublicServer
	})

	serverStartControlPlane = func(stop <-chan struct{}, listen func(network, address string) (net.Listener, error)) error {
		return nil
	}
	serverStartAdminServer = func(stop <-chan struct{}, serve func(addr string, handler http.Handler) error) <-chan error {
		return make(chan error)
	}
	serverStartPublicServer = func(stop <-chan struct{}, serve func(addr string, handler http.Handler) error, serveTLS func(addr, certFile, keyFile string, handler http.Handler) error) <-chan error {
		errs := make(chan error, 1)
		errs <- errors.New("public http failed")
		return errs
	}

	code := runMain(io.Discard, func() (*config.ServerConfig, error) {
		return &config.ServerConfig{
			BaseDomain:     "localhost",
			PublicPort:     8080,
			AdminPort:      8081,
			ControlPort:    7000,
			PortRangeStart: 33000,
			PortRangeEnd:   33010,
		}, nil
	}, nil, nil, nil)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}

func TestRunMainPublicHTTPSServerError(t *testing.T) {
	oldCfg := cfg
	oldStartControlPlane := serverStartControlPlane
	oldStartAdminServer := serverStartAdminServer
	oldStartPublicServer := serverStartPublicServer
	t.Cleanup(func() {
		cfg = oldCfg
		serverStartControlPlane = oldStartControlPlane
		serverStartAdminServer = oldStartAdminServer
		serverStartPublicServer = oldStartPublicServer
	})

	serverStartControlPlane = func(stop <-chan struct{}, listen func(network, address string) (net.Listener, error)) error {
		return nil
	}
	serverStartAdminServer = func(stop <-chan struct{}, serve func(addr string, handler http.Handler) error) <-chan error {
		return make(chan error)
	}
	serverStartPublicServer = func(stop <-chan struct{}, serve func(addr string, handler http.Handler) error, serveTLS func(addr, certFile, keyFile string, handler http.Handler) error) <-chan error {
		errs := make(chan error, 1)
		errs <- errors.New("public https failed")
		return errs
	}

	code := runMain(io.Discard, func() (*config.ServerConfig, error) {
		return &config.ServerConfig{
			BaseDomain:     "localhost",
			PublicPort:     443,
			AdminPort:      8081,
			ControlPort:    7000,
			PortRangeStart: 33000,
			PortRangeEnd:   33010,
			TLSEnabled:     true,
		}, nil
	}, nil, nil, nil)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}

func TestRunMainReturnsZeroOnServerNilError(t *testing.T) {
	oldCfg := cfg
	oldStartControlPlane := serverStartControlPlane
	oldStartAdminServer := serverStartAdminServer
	oldStartPublicServer := serverStartPublicServer
	t.Cleanup(func() {
		cfg = oldCfg
		serverStartControlPlane = oldStartControlPlane
		serverStartAdminServer = oldStartAdminServer
		serverStartPublicServer = oldStartPublicServer
	})

	serverStartControlPlane = func(stop <-chan struct{}, listen func(network, address string) (net.Listener, error)) error {
		return nil
	}
	serverStartAdminServer = func(stop <-chan struct{}, serve func(addr string, handler http.Handler) error) <-chan error {
		errs := make(chan error, 1)
		errs <- nil
		return errs
	}
	serverStartPublicServer = func(stop <-chan struct{}, serve func(addr string, handler http.Handler) error, serveTLS func(addr, certFile, keyFile string, handler http.Handler) error) <-chan error {
		return make(chan error)
	}

	code := runMain(io.Discard, func() (*config.ServerConfig, error) {
		return &config.ServerConfig{
			BaseDomain:     "localhost",
			PublicPort:     8080,
			AdminPort:      8081,
			ControlPort:    7000,
			PortRangeStart: 33000,
			PortRangeEnd:   33010,
		}, nil
	}, nil, nil, nil)

	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
}

func TestStartAdminServer(t *testing.T) {
	oldCfg := cfg
	cfg = &config.ServerConfig{AdminPort: 8081}
	t.Cleanup(func() { cfg = oldCfg })

	stop := make(chan struct{})
	called := make(chan struct{})

	errs := startAdminServer(stop, func(addr string, handler http.Handler) error {
		if addr != cfg.AdminAddr() {
			t.Fatalf("addr = %q, want %q", addr, cfg.AdminAddr())
		}

		req := httptest.NewRequest(http.MethodGet, "/api/tunnels", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}

		close(called)
		<-stop
		return nil
	})

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("admin server was not started")
	}

	close(stop)
	if err := <-errs; err != nil {
		t.Fatalf("admin error = %v, want nil", err)
	}
}

func TestStartAdminServerReturnsServeError(t *testing.T) {
	oldCfg := cfg
	cfg = &config.ServerConfig{AdminPort: 8081}
	t.Cleanup(func() { cfg = oldCfg })

	wantErr := errors.New("admin failed")
	errs := startAdminServer(make(chan struct{}), func(addr string, handler http.Handler) error {
		return wantErr
	})

	if err := <-errs; !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestStartPublicServerHTTP(t *testing.T) {
	oldCfg := cfg
	cfg = &config.ServerConfig{BaseDomain: "localhost", PublicPort: 8080}
	t.Cleanup(func() { cfg = oldCfg })

	stop := make(chan struct{})
	called := make(chan struct{})

	errs := startPublicServer(
		stop,
		func(addr string, handler http.Handler) error {
			if addr != cfg.PublicAddr() {
				t.Fatalf("addr = %q, want %q", addr, cfg.PublicAddr())
			}

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = "localhost:8080"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			close(called)
			<-stop
			return nil
		},
		func(string, string, string, http.Handler) error {
			t.Fatal("serveTLS should not be called")
			return nil
		},
	)

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("public HTTP server was not started")
	}

	close(stop)
	if err := <-errs; err != nil {
		t.Fatalf("public HTTP error = %v, want nil", err)
	}
}

func TestStartPublicServerHTTPReturnsServeError(t *testing.T) {
	oldCfg := cfg
	cfg = &config.ServerConfig{BaseDomain: "localhost", PublicPort: 8080}
	t.Cleanup(func() { cfg = oldCfg })

	wantErr := errors.New("http failed")
	errs := startPublicServer(
		make(chan struct{}),
		func(addr string, handler http.Handler) error { return wantErr },
		func(string, string, string, http.Handler) error {
			t.Fatal("serveTLS should not be called")
			return nil
		},
	)

	if err := <-errs; !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestStartPublicServerHTTPS(t *testing.T) {
	oldCfg := cfg
	cfg = &config.ServerConfig{
		BaseDomain:  "localhost",
		PublicPort:  443,
		TLSEnabled:  true,
		TLSCertFile: "cert.pem",
		TLSKeyFile:  "key.pem",
	}
	t.Cleanup(func() { cfg = oldCfg })

	stop := make(chan struct{})
	redirectCalled := make(chan struct{})
	httpsCalled := make(chan struct{})

	errs := startPublicServer(
		stop,
		func(addr string, handler http.Handler) error {
			if addr != ":80" {
				t.Fatalf("redirect addr = %q, want %q", addr, ":80")
			}

			req := httptest.NewRequest(http.MethodGet, "http://ratatosk.localhost/docs?a=1", nil)
			req.Host = "ratatosk.localhost"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusMovedPermanently {
				t.Fatalf("redirect status = %d, want %d", w.Code, http.StatusMovedPermanently)
			}
			if location := w.Header().Get("Location"); location != "https://ratatosk.localhost/docs?a=1" {
				t.Fatalf("Location = %q, want %q", location, "https://ratatosk.localhost/docs?a=1")
			}

			close(redirectCalled)
			<-stop
			return nil
		},
		func(addr, certFile, keyFile string, handler http.Handler) error {
			if addr != cfg.PublicAddr() {
				t.Fatalf("addr = %q, want %q", addr, cfg.PublicAddr())
			}
			if certFile != "cert.pem" {
				t.Fatalf("certFile = %q, want %q", certFile, "cert.pem")
			}
			if keyFile != "key.pem" {
				t.Fatalf("keyFile = %q, want %q", keyFile, "key.pem")
			}

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = "localhost:443"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			close(httpsCalled)
			<-stop
			return nil
		},
	)

	select {
	case <-redirectCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("redirect server was not started")
	}

	select {
	case <-httpsCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("public HTTPS server was not started")
	}

	close(stop)
	if err := <-errs; err != nil {
		t.Fatalf("public HTTPS error = %v, want nil", err)
	}
}

func TestStartPublicServerHTTPSReturnsServeTLSError(t *testing.T) {
	oldCfg := cfg
	cfg = &config.ServerConfig{
		BaseDomain:  "localhost",
		PublicPort:  443,
		TLSEnabled:  true,
		TLSCertFile: "cert.pem",
		TLSKeyFile:  "key.pem",
	}
	t.Cleanup(func() { cfg = oldCfg })

	redirectCalled := make(chan struct{})
	wantErr := errors.New("https failed")

	errs := startPublicServer(
		make(chan struct{}),
		func(addr string, handler http.Handler) error {
			close(redirectCalled)
			return nil
		},
		func(addr, certFile, keyFile string, handler http.Handler) error {
			<-redirectCalled
			return wantErr
		},
	)

	if err := <-errs; !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestStartPublicServerHTTPSRedirectError(t *testing.T) {
	oldCfg := cfg
	cfg = &config.ServerConfig{
		BaseDomain:  "localhost",
		PublicPort:  443,
		TLSEnabled:  true,
		TLSCertFile: "cert.pem",
		TLSKeyFile:  "key.pem",
	}
	t.Cleanup(func() { cfg = oldCfg })

	redirectCalled := make(chan struct{})

	errs := startPublicServer(
		make(chan struct{}),
		func(addr string, handler http.Handler) error {
			close(redirectCalled)
			return errors.New("redirect failed")
		},
		func(addr, certFile, keyFile string, handler http.Handler) error {
			<-redirectCalled
			return nil
		},
	)

	if err := <-errs; err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestStartPublicServerAutoTLS(t *testing.T) {
	oldCfg := cfg
	oldServeCertmagic := mainServeCertmagic
	cfg = &config.ServerConfig{
		BaseDomain:  "tunnel.example.com",
		PublicPort:  443,
		TLSAuto:     true,
		TLSEmail:    "admin@example.com",
		TLSProvider: "cloudflare",
		TLSAPIToken: "test-token",
	}
	t.Cleanup(func() {
		cfg = oldCfg
		mainServeCertmagic = oldServeCertmagic
	})

	stop := make(chan struct{})
	called := make(chan struct{})

	mainServeCertmagic = func(cmCfg autocert.Config, handler http.Handler) error {
		if cmCfg.Email != "admin@example.com" {
			t.Fatalf("email = %q, want %q", cmCfg.Email, "admin@example.com")
		}
		if cmCfg.Provider != "cloudflare" {
			t.Fatalf("provider = %q, want %q", cmCfg.Provider, "cloudflare")
		}
		if cmCfg.APIToken != "test-token" {
			t.Fatalf("api_token = %q, want %q", cmCfg.APIToken, "test-token")
		}
		if len(cmCfg.Domains) != 2 {
			t.Fatalf("domains = %v, want 2 entries", cmCfg.Domains)
		}
		if cmCfg.Domains[0] != "tunnel.example.com" {
			t.Fatalf("domains[0] = %q, want %q", cmCfg.Domains[0], "tunnel.example.com")
		}
		if cmCfg.Domains[1] != "*.tunnel.example.com" {
			t.Fatalf("domains[1] = %q, want %q", cmCfg.Domains[1], "*.tunnel.example.com")
		}

		// Verify handler is the HTTP proxy handler.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "localhost:443"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}

		close(called)
		<-stop
		return nil
	}

	errs := startPublicServer(
		stop,
		func(string, http.Handler) error {
			t.Fatal("serve should not be called")
			return nil
		},
		func(string, string, string, http.Handler) error {
			t.Fatal("serveTLS should not be called")
			return nil
		},
	)

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("certmagic server was not started")
	}

	close(stop)
	if err := <-errs; err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestStartPublicServerAutoTLSReturnsError(t *testing.T) {
	oldCfg := cfg
	oldServeCertmagic := mainServeCertmagic
	cfg = &config.ServerConfig{
		BaseDomain:  "tunnel.example.com",
		PublicPort:  443,
		TLSAuto:     true,
		TLSEmail:    "admin@example.com",
		TLSProvider: "cloudflare",
		TLSAPIToken: "test-token",
	}
	t.Cleanup(func() {
		cfg = oldCfg
		mainServeCertmagic = oldServeCertmagic
	})

	wantErr := errors.New("certmagic failed")
	mainServeCertmagic = func(cmCfg autocert.Config, handler http.Handler) error {
		return wantErr
	}

	errs := startPublicServer(
		make(chan struct{}),
		func(string, http.Handler) error {
			t.Fatal("serve should not be called")
			return nil
		},
		func(string, string, string, http.Handler) error {
			t.Fatal("serveTLS should not be called")
			return nil
		},
	)

	if err := <-errs; !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

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
	registry.Register(subdomain, serverSession, basicAuth, protocol.ProtoHTTP)
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
	local := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	local := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	local := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	local := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestAdminDashboardSubErrorFallback(t *testing.T) {
	handler := newAdminHandlerFS(registry, brokenFS{}, "dev", noopCheckUpdate)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Dashboard not built. Run: make build") {
		t.Fatalf("body = %q, want dashboard placeholder", w.Body.String())
	}
}

func TestAdminDashboardEmbeddedFS(t *testing.T) {
	handler := newAdminHandlerFS(registry, fstest.MapFS{
		"dashboard/dist/index.html":     {Data: []byte("<html>dashboard</html>")},
		"dashboard/dist/assets/app.js":  {Data: []byte("console.log('ok')")},
		"dashboard/dist/assets/app.css": {Data: []byte("body { color: red; }")},
		"dashboard/dist/favicon.svg":    {Data: []byte("<svg></svg>")},
		"dashboard/dist/icons.svg":      {Data: []byte("<svg></svg>")},
	}, "dev", noopCheckUpdate)

	t.Run("root", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if !strings.Contains(w.Body.String(), "dashboard") {
			t.Fatalf("body = %q, want embedded dashboard", w.Body.String())
		}
	})

	t.Run("spa route", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/settings/profile", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if !strings.Contains(w.Body.String(), "dashboard") {
			t.Fatalf("body = %q, want SPA fallback", w.Body.String())
		}
	})
}

func TestAdminDashboardMissingIndexFallback(t *testing.T) {
	handler := newAdminHandlerFS(registry, fstest.MapFS{
		"dashboard/dist/assets/app.js": {Data: []byte("console.log('ok')")},
	}, "dev", noopCheckUpdate)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Dashboard not built. Run: make build") {
		t.Fatalf("body = %q, want dashboard placeholder", w.Body.String())
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

func TestHTTPProxySessionClosed(t *testing.T) {
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

	registry.Register("closed", serverSession, "", protocol.ProtoHTTP)
	t.Cleanup(func() { registry.Unregister("closed") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "closed.localhost:8080"

	handleHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}

func TestHTTPProxyWriteError(t *testing.T) {
	clientPipe, serverPipe := net.Pipe()
	serverSession, err := tunnel.NewServerSession(serverPipe)
	if err != nil {
		t.Fatalf("NewServerSession: %v", err)
	}
	clientSession, err := tunnel.NewClientSession(clientPipe)
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	t.Cleanup(func() {
		clientSession.Close()
		serverSession.Close()
		clientPipe.Close()
		serverPipe.Close()
	})

	go func() {
		stream, err := clientSession.Accept()
		if err != nil {
			return
		}
		defer stream.Close()
		io.Copy(io.Discard, stream)
	}()

	registry.Register("broken", serverSession, "", protocol.ProtoHTTP)
	t.Cleanup(func() { registry.Unregister("broken") })

	w := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodPost, "http://broken.localhost:8080/upload", failingReadCloser{})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = "broken.localhost:8080"
	req.ContentLength = 1

	handleHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
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
	registry.Register("drop-req", serverSession, "", protocol.ProtoHTTP)
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
	local := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	local := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// TestHTTPProxyPOSTKeepAlive verifies that a POST request with a body
// followed by a GET on the same keep-alive connection both succeed.
// This catches a bug where a background io.Copy goroutine in proxyRequest
// reads from the client connection after r.Write already consumed the body,
// stealing bytes from the next keep-alive request.
func TestHTTPProxyPOSTKeepAlive(t *testing.T) {
	local := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		if r.Method == "POST" {
			fmt.Fprintf(w, "post:%s", body)
		} else {
			fmt.Fprintf(w, "get:%s", r.URL.Path)
		}
	}))
	defer local.Close()

	setupTunnel(t, "post-ka", local.Listener.Addr().String())
	proxyAddr := startProxyServer(t)

	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	reader := bufio.NewReader(conn)

	// First request: POST with a body, keep-alive (default for HTTP/1.1).
	body := "key=value"
	fmt.Fprintf(conn, "POST /upload HTTP/1.1\r\nHost: post-ka.localhost:8080\r\nContent-Length: %d\r\n\r\n%s", len(body), body)

	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("request 1 (POST): ReadResponse: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := string(respBody); got != "post:key=value" {
		t.Fatalf("request 1: body = %q, want %q", got, "post:key=value")
	}

	// Second request: GET on the same keep-alive connection.
	// If the POST's background io.Copy goroutine consumed bytes from the
	// connection, this request will be corrupted or never receive a response.
	fmt.Fprintf(conn, "GET /next HTTP/1.1\r\nHost: post-ka.localhost:8080\r\n\r\n")

	resp2, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("request 2 (GET): ReadResponse: %v (keep-alive broken after POST)", err)
	}
	respBody2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if got := string(respBody2); got != "get:/next" {
		t.Errorf("request 2: body = %q, want %q", got, "get:/next")
	}
}

func TestHTTPProxyKeepAliveInvalidHost(t *testing.T) {
	local := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestHTTPProxyKeepAliveBasicAuthRejected(t *testing.T) {
	local := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer local.Close()

	setupTunnelWithAuth(t, "auth-keepalive", local.Listener.Addr().String(), "admin:secret")
	proxyAddr := startProxyServer(t)

	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	reader := bufio.NewReader(conn)

	fmt.Fprintf(conn, "GET /page HTTP/1.1\r\nHost: auth-keepalive.localhost:8080\r\nAuthorization: Basic YWRtaW46c2VjcmV0\r\n\r\n")
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("request 1: ReadResponse: %v", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	fmt.Fprintf(conn, "GET /script.js HTTP/1.1\r\nHost: auth-keepalive.localhost:8080\r\n\r\n")
	resp2, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("request 2: ReadResponse: %v", err)
	}
	body, err := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if err != nil {
		t.Fatalf("request 2: ReadAll: %v", err)
	}

	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("request 2: status = %d, want %d", resp2.StatusCode, http.StatusUnauthorized)
	}
	if got := strings.TrimSpace(string(body)); got != "Unauthorized" {
		t.Fatalf("request 2: body = %q, want %q", got, "Unauthorized")
	}
}

func TestHTTPProxyBasicAuthRejected(t *testing.T) {
	// Register a tunnel with basic auth required.
	local := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	local := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestHandleConnectionTCPTunnel(t *testing.T) {
	oldAlloc := portAlloc
	portAlloc = tunnel.NewPortAllocator(33000, 33100)
	t.Cleanup(func() { portAlloc = oldAlloc })

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
		t.Fatalf("Open control stream: %v", err)
	}

	req := &protocol.TunnelRequest{Protocol: protocol.ProtoTCP, LocalPort: 22}
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
	if resp.Port == 0 {
		t.Fatal("expected non-zero port in response")
	}
	if resp.Subdomain != "" {
		t.Errorf("expected empty subdomain for TCP tunnel, got %q", resp.Subdomain)
	}

	// Verify registered in port registry.
	if _, ok := registry.GetPortEntry(resp.Port); !ok {
		t.Fatalf("port %d not found in registry", resp.Port)
	}

	// Client side: accept streams and echo data back.
	go func() {
		for {
			stream, err := clientSession.Accept()
			if err != nil {
				return
			}
			go func() {
				defer stream.Close()
				io.Copy(stream, stream)
			}()
		}
	}()

	// Connect to the allocated port and verify data flows.
	tcpConn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", resp.Port), 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer tcpConn.Close()

	msg := []byte("hello TCP tunnel")
	if _, err := tcpConn.Write(msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, len(msg))
	tcpConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(tcpConn, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(buf) != string(msg) {
		t.Errorf("got %q, want %q", buf, msg)
	}

	// Close client session and verify cleanup.
	tcpConn.Close()
	clientSession.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleConnection did not return after client disconnect")
	}

	if _, ok := registry.GetPortEntry(resp.Port); ok {
		t.Fatalf("port %d still in registry after disconnect", resp.Port)
	}
}

func TestHandleConnectionUDPTunnel(t *testing.T) {
	oldAlloc := portAlloc
	portAlloc = tunnel.NewPortAllocator(34000, 34100)
	t.Cleanup(func() { portAlloc = oldAlloc })

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
		t.Fatalf("Open control stream: %v", err)
	}

	req := &protocol.TunnelRequest{Protocol: protocol.ProtoUDP, LocalPort: 25565}
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
	if resp.Port == 0 {
		t.Fatal("expected non-zero port in response")
	}

	// Verify registered in port registry.
	if _, ok := registry.GetPortEntry(resp.Port); !ok {
		t.Fatalf("port %d not found in registry", resp.Port)
	}

	// Client side: accept streams, read framed data, and echo back.
	go func() {
		for {
			stream, err := clientSession.Accept()
			if err != nil {
				return
			}
			go func() {
				defer stream.Close()
				for {
					data, err := tunnel.ReadFrame(stream)
					if err != nil {
						return
					}
					tunnel.WriteFrame(stream, data)
				}
			}()
		}
	}()

	// Send a UDP datagram to the allocated port.
	udpAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("127.0.0.1:%d", resp.Port))
	udpConn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer udpConn.Close()

	msg := []byte("hello UDP tunnel")
	if _, err := udpConn.Write(msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, 65535)
	udpConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := udpConn.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != string(msg) {
		t.Errorf("got %q, want %q", buf[:n], msg)
	}

	// Close client session and verify cleanup.
	clientSession.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleConnection did not return after client disconnect")
	}

	if _, ok := registry.GetPortEntry(resp.Port); ok {
		t.Fatalf("port %d still in registry after disconnect", resp.Port)
	}
}

func TestHandleConnectionUnsupportedProtocol(t *testing.T) {
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

	req := &protocol.TunnelRequest{Protocol: "ftp", LocalPort: 21}
	if err := protocol.WriteRequest(controlStream, req); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	resp, err := protocol.ReadResponse(controlStream)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	controlStream.Close()

	if resp.Success {
		t.Fatal("expected failure for unsupported protocol")
	}
	if !strings.Contains(resp.Error, "unsupported protocol") {
		t.Errorf("error = %q, want to contain 'unsupported protocol'", resp.Error)
	}

	clientSession.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleConnection did not return")
	}
}

func TestHandleHTTPTunnelUniqueSubdomainFailure(t *testing.T) {
	oldRegistry := registry
	oldGenerateSubdomain := serverGenerateSubdomain
	registry = tunnel.NewRegistry()
	serverGenerateSubdomain = func() string { return "taken" }
	t.Cleanup(func() {
		registry = oldRegistry
		serverGenerateSubdomain = oldGenerateSubdomain
	})

	registry.Register("taken", nil, "", protocol.ProtoHTTP)

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleHTTPTunnel(nil, serverConn, &protocol.TunnelRequest{}, "remote")
	}()

	resp, err := protocol.ReadResponse(clientConn)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	clientConn.Close()

	if resp.Success {
		t.Fatal("expected subdomain generation failure")
	}
	if !strings.Contains(resp.Error, "failed to generate unique subdomain") {
		t.Fatalf("error = %q, want unique subdomain failure", resp.Error)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleHTTPTunnel did not return")
	}
}

func TestHandleHTTPTunnelWriteResponseErrorUnregistersTunnel(t *testing.T) {
	oldRegistry := registry
	oldGenerateSubdomain := serverGenerateSubdomain
	registry = tunnel.NewRegistry()
	serverGenerateSubdomain = func() string { return "write-fail" }
	t.Cleanup(func() {
		registry = oldRegistry
		serverGenerateSubdomain = oldGenerateSubdomain
	})

	clientConn, serverConn := net.Pipe()
	clientConn.Close()

	handleHTTPTunnel(nil, serverConn, &protocol.TunnelRequest{BasicAuth: "admin:secret"}, "remote")

	if registry.HasSubdomain("write-fail") {
		t.Fatal("tunnel remained registered after response write failure")
	}
}

func TestHandleTCPTunnelAllocationFailure(t *testing.T) {
	oldAlloc := portAlloc
	portAlloc = tunnel.NewPortAllocator(35010, 35011)
	if _, err := portAlloc.Allocate(); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	t.Cleanup(func() { portAlloc = oldAlloc })

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTCPTunnel(nil, serverConn, &protocol.TunnelRequest{LocalPort: 22}, "remote")
	}()

	resp, err := protocol.ReadResponse(clientConn)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	clientConn.Close()

	if resp.Success {
		t.Fatal("expected allocation failure")
	}
	if !strings.Contains(resp.Error, "port allocation failed") {
		t.Fatalf("error = %q, want allocation failure", resp.Error)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleTCPTunnel did not return")
	}
}

func TestHandleTCPTunnelListenFailure(t *testing.T) {
	oldAlloc := portAlloc
	oldListenTCP := serverListenTCP
	portAlloc = tunnel.NewPortAllocator(35011, 35012)
	serverListenTCP = func(network, address string) (net.Listener, error) {
		return nil, errors.New("listen failed")
	}
	t.Cleanup(func() {
		portAlloc = oldAlloc
		serverListenTCP = oldListenTCP
	})

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTCPTunnel(nil, serverConn, &protocol.TunnelRequest{LocalPort: 22}, "remote")
	}()

	resp, err := protocol.ReadResponse(clientConn)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	clientConn.Close()

	if resp.Success {
		t.Fatal("expected listen failure")
	}
	if !strings.Contains(resp.Error, "failed to listen on port") {
		t.Fatalf("error = %q, want listen failure", resp.Error)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleTCPTunnel did not return")
	}
}

func TestHandleTCPTunnelWriteResponseErrorClosesListener(t *testing.T) {
	oldAlloc := portAlloc
	oldRegistry := registry
	oldListenTCP := serverListenTCP
	portAlloc = tunnel.NewPortAllocator(35012, 35013)
	registry = tunnel.NewRegistry()
	serverListenTCP = net.Listen
	t.Cleanup(func() {
		portAlloc = oldAlloc
		registry = oldRegistry
		serverListenTCP = oldListenTCP
	})

	clientConn, serverConn := net.Pipe()
	clientConn.Close()

	handleTCPTunnel(nil, serverConn, &protocol.TunnelRequest{LocalPort: 22}, "remote")

	if _, ok := registry.GetPortEntry(35012); ok {
		t.Fatal("port remained registered after response write failure")
	}
	if got, err := portAlloc.Allocate(); err != nil || got != 35012 {
		t.Fatalf("Allocate() = (%d, %v), want (35012, nil)", got, err)
	}

	ln, err := net.Listen("tcp", ":35012")
	if err != nil {
		t.Fatalf("listener was not closed: %v", err)
	}
	ln.Close()
}

func TestHandleUDPTunnelAllocationFailure(t *testing.T) {
	oldAlloc := portAlloc
	portAlloc = tunnel.NewPortAllocator(35020, 35021)
	if _, err := portAlloc.Allocate(); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	t.Cleanup(func() { portAlloc = oldAlloc })

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleUDPTunnel(nil, serverConn, &protocol.TunnelRequest{LocalPort: 25565}, "remote")
	}()

	resp, err := protocol.ReadResponse(clientConn)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	clientConn.Close()

	if resp.Success {
		t.Fatal("expected allocation failure")
	}
	if !strings.Contains(resp.Error, "port allocation failed") {
		t.Fatalf("error = %q, want allocation failure", resp.Error)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleUDPTunnel did not return")
	}
}

func TestHandleUDPTunnelResolveFailure(t *testing.T) {
	oldAlloc := portAlloc
	oldResolveUDPAddr := serverResolveUDPAddr
	portAlloc = tunnel.NewPortAllocator(35021, 35022)
	serverResolveUDPAddr = func(network, address string) (*net.UDPAddr, error) {
		return nil, errors.New("resolve failed")
	}
	t.Cleanup(func() {
		portAlloc = oldAlloc
		serverResolveUDPAddr = oldResolveUDPAddr
	})

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleUDPTunnel(nil, serverConn, &protocol.TunnelRequest{LocalPort: 25565}, "remote")
	}()

	resp, err := protocol.ReadResponse(clientConn)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	clientConn.Close()

	if resp.Success {
		t.Fatal("expected resolve failure")
	}
	if !strings.Contains(resp.Error, "failed to resolve UDP address") {
		t.Fatalf("error = %q, want resolve failure", resp.Error)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleUDPTunnel did not return")
	}
}

func TestHandleUDPTunnelListenFailure(t *testing.T) {
	oldAlloc := portAlloc
	oldResolveUDPAddr := serverResolveUDPAddr
	oldListenUDP := serverListenUDP
	portAlloc = tunnel.NewPortAllocator(35022, 35023)
	serverResolveUDPAddr = net.ResolveUDPAddr
	serverListenUDP = net.ListenUDP
	t.Cleanup(func() {
		portAlloc = oldAlloc
		serverResolveUDPAddr = oldResolveUDPAddr
		serverListenUDP = oldListenUDP
	})

	busy, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 35022})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer busy.Close()

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleUDPTunnel(nil, serverConn, &protocol.TunnelRequest{LocalPort: 25565}, "remote")
	}()

	resp, err := protocol.ReadResponse(clientConn)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	clientConn.Close()

	if resp.Success {
		t.Fatal("expected listen failure")
	}
	if !strings.Contains(resp.Error, "failed to listen on UDP port 35022") {
		t.Fatalf("error = %q, want listen failure", resp.Error)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleUDPTunnel did not return")
	}
}

func TestHandleUDPTunnelWriteResponseErrorClosesSocket(t *testing.T) {
	oldAlloc := portAlloc
	oldRegistry := registry
	oldResolveUDPAddr := serverResolveUDPAddr
	oldListenUDP := serverListenUDP
	portAlloc = tunnel.NewPortAllocator(35023, 35024)
	registry = tunnel.NewRegistry()
	serverResolveUDPAddr = net.ResolveUDPAddr
	serverListenUDP = net.ListenUDP
	t.Cleanup(func() {
		portAlloc = oldAlloc
		registry = oldRegistry
		serverResolveUDPAddr = oldResolveUDPAddr
		serverListenUDP = oldListenUDP
	})

	clientConn, serverConn := net.Pipe()
	clientConn.Close()

	handleUDPTunnel(nil, serverConn, &protocol.TunnelRequest{LocalPort: 25565}, "remote")

	if _, ok := registry.GetPortEntry(35023); ok {
		t.Fatal("port remained registered after response write failure")
	}
	if got, err := portAlloc.Allocate(); err != nil || got != 35023 {
		t.Fatalf("Allocate() = (%d, %v), want (35023, nil)", got, err)
	}

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 35023})
	if err != nil {
		t.Fatalf("UDP socket was not closed: %v", err)
	}
	udpConn.Close()
}

func TestHTTPProxyBasicAuthWrongCredentials(t *testing.T) {
	local := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	local := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestAdminAPIVersionUpToDate(t *testing.T) {
	handler := newAdminHandlerFS(registry, fstest.MapFS{
		"dashboard/dist/index.html": {Data: []byte("<html></html>")},
	}, "v1.0.0", func(string) string { return "" })

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp versionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Version != "v1.0.0" {
		t.Fatalf("version = %q, want %q", resp.Version, "v1.0.0")
	}
	if resp.UpdateAvail {
		t.Fatal("update_available = true, want false")
	}
	if resp.LatestVersion != "" {
		t.Fatalf("latest_version = %q, want empty", resp.LatestVersion)
	}
}

func TestAdminAPIVersionUpdateAvailable(t *testing.T) {
	handler := newAdminHandlerFS(registry, fstest.MapFS{
		"dashboard/dist/index.html": {Data: []byte("<html></html>")},
	}, "v1.0.0", func(string) string { return "v2.0.0" })

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp versionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Version != "v1.0.0" {
		t.Fatalf("version = %q, want %q", resp.Version, "v1.0.0")
	}
	if !resp.UpdateAvail {
		t.Fatal("update_available = false, want true")
	}
	if resp.LatestVersion != "v2.0.0" {
		t.Fatalf("latest_version = %q, want %q", resp.LatestVersion, "v2.0.0")
	}
}

func TestAdminAPIVersionDevBuild(t *testing.T) {
	handler := newAdminHandlerFS(registry, fstest.MapFS{
		"dashboard/dist/index.html": {Data: []byte("<html></html>")},
	}, "dev", func(v string) string {
		if v != "dev" {
			t.Fatalf("checkUpdate received %q, want %q", v, "dev")
		}
		return ""
	})

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp versionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Version != "dev" {
		t.Fatalf("version = %q, want %q", resp.Version, "dev")
	}
	if resp.UpdateAvail {
		t.Fatal("update_available = true, want false for dev build")
	}
}

func TestAdminAPIVersionContentType(t *testing.T) {
	handler := newAdminHandlerFS(registry, fstest.MapFS{
		"dashboard/dist/index.html": {Data: []byte("<html></html>")},
	}, "v1.0.0", noopCheckUpdate)

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
}

func TestRunMainStartupUpdateCheck(t *testing.T) {
	oldCfg := cfg
	oldCheckUpdate := serverCheckUpdate
	oldStartControlPlane := serverStartControlPlane
	oldStartAdminServer := serverStartAdminServer
	oldStartPublicServer := serverStartPublicServer
	oldVersion := Version
	t.Cleanup(func() {
		cfg = oldCfg
		serverCheckUpdate = oldCheckUpdate
		serverStartControlPlane = oldStartControlPlane
		serverStartAdminServer = oldStartAdminServer
		serverStartPublicServer = oldStartPublicServer
		Version = oldVersion
	})

	checked := make(chan string, 1)
	serverCheckUpdate = func(v string) string {
		checked <- v
		return "v9.0.0"
	}
	Version = "v1.0.0"

	serverStartControlPlane = func(stop <-chan struct{}, listen func(string, string) (net.Listener, error)) error {
		return nil
	}
	adminErrs := make(chan error, 1)
	serverStartAdminServer = func(stop <-chan struct{}, serve func(string, http.Handler) error) <-chan error {
		return adminErrs
	}
	serverStartPublicServer = func(stop <-chan struct{}, serve func(string, http.Handler) error, serveTLS func(string, string, string, http.Handler) error) <-chan error {
		return make(chan error, 1)
	}

	var stdout bytes.Buffer
	done := make(chan struct{})
	go func() {
		runMain(&stdout, func() (*config.ServerConfig, error) {
			return &config.ServerConfig{
				BaseDomain:     "localhost",
				PublicPort:     8080,
				AdminPort:      8081,
				ControlPort:    7000,
				PortRangeStart: 34000,
				PortRangeEnd:   34010,
			}, nil
		}, nil, nil, nil)
		close(done)
	}()

	select {
	case v := <-checked:
		if v != "v1.0.0" {
			t.Fatalf("checkUpdate received %q, want %q", v, "v1.0.0")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startup update check was not called")
	}

	// Unblock runMain so the goroutine completes before cleanup.
	adminErrs <- nil
	<-done
}
