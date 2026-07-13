package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	autocert "ratatosk/internal/certmagic"
	"ratatosk/internal/config"
	"ratatosk/internal/control"
	"ratatosk/internal/inspector"
	"ratatosk/internal/protocol"
	"ratatosk/internal/tunnel"
)

var noopCheckUpdate = func(string) string { return "" }

type tunnelListStub []tunnel.TunnelInfo

func (s tunnelListStub) ListTunnels() []tunnel.TunnelInfo {
	return s
}

type fixedPortAllocator struct {
	port int
}

func (a fixedPortAllocator) Allocate() (int, error) {
	return a.port, nil
}

func (fixedPortAllocator) Release(int) {}

type fakeCertificateManager struct {
	tlsConfig *tls.Config
	serve     func(http.Handler) error
}

func (m *fakeCertificateManager) TLSConfig() *tls.Config {
	return m.tlsConfig
}

func (m *fakeCertificateManager) Serve(handler http.Handler) error {
	if m.serve == nil {
		return nil
	}
	return m.serve(handler)
}

func generateTestCertificate(t *testing.T) (tls.Certificate, string, string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("WriteFile certificate: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("WriteFile private key: %v", err)
	}

	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	return certificate, certFile, keyFile
}

// freePort finds an available TCP port by binding to :0 and returning
// the port the OS assigned. This avoids hardcoded ports that may be
// occupied in CI.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("could not find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

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

type singleConnListener struct {
	conn      net.Conn
	closed    chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	sent      bool
}

func newSingleConnListener(conn net.Conn) *singleConnListener {
	return &singleConnListener{
		conn:   conn,
		closed: make(chan struct{}),
	}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if !l.sent {
		l.sent = true
		l.mu.Unlock()
		return l.conn, nil
	}
	l.mu.Unlock()

	<-l.closed
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	return stubAddr("127.0.0.1:0")
}

type trackingConn struct {
	net.Conn
	closed       chan struct{}
	deadlineSet  chan struct{}
	closeOnce    sync.Once
	deadlineOnce sync.Once
}

func newTrackingConn(conn net.Conn) *trackingConn {
	return &trackingConn{
		Conn:        conn,
		closed:      make(chan struct{}),
		deadlineSet: make(chan struct{}),
	}
}

func (c *trackingConn) Close() error {
	err := c.Conn.Close()
	c.closeOnce.Do(func() { close(c.closed) })
	return err
}

func (c *trackingConn) SetDeadline(deadline time.Time) error {
	c.deadlineOnce.Do(func() { close(c.deadlineSet) })
	return c.Conn.SetDeadline(deadline)
}

type deadlineErrorConn struct {
	net.Conn
	err error
}

func (c deadlineErrorConn) SetDeadline(time.Time) error {
	return c.err
}

type deadlineSequenceConn struct {
	net.Conn
	mu      sync.Mutex
	calls   int
	failAt  int
	failErr error
}

func (c *deadlineSequenceConn) SetDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	if call == c.failAt {
		return c.failErr
	}
	return c.Conn.SetDeadline(deadline)
}

type controlTestConn struct {
	closed        chan struct{}
	readCalled    chan struct{}
	remoteCalled  chan struct{}
	remoteRelease <-chan struct{}
	closeOnce     sync.Once
	readOnce      sync.Once
	remoteOnce    sync.Once
}

func newControlTestConn(remoteRelease <-chan struct{}) *controlTestConn {
	return &controlTestConn{
		closed:        make(chan struct{}),
		readCalled:    make(chan struct{}),
		remoteCalled:  make(chan struct{}),
		remoteRelease: remoteRelease,
	}
}

func (c *controlTestConn) Read([]byte) (int, error) {
	c.readOnce.Do(func() { close(c.readCalled) })
	<-c.closed
	return 0, net.ErrClosed
}

func (c *controlTestConn) Write(payload []byte) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
		return len(payload), nil
	}
}

func (c *controlTestConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *controlTestConn) LocalAddr() net.Addr {
	return stubAddr("127.0.0.1:7000")
}

func (c *controlTestConn) RemoteAddr() net.Addr {
	c.remoteOnce.Do(func() { close(c.remoteCalled) })
	if c.remoteRelease != nil {
		<-c.remoteRelease
	}
	return stubAddr("127.0.0.1:40000")
}

func (c *controlTestConn) SetDeadline(time.Time) error {
	return nil
}

func (*controlTestConn) SetReadDeadline(time.Time) error {
	return nil
}

func (*controlTestConn) SetWriteDeadline(time.Time) error {
	return nil
}

type controlTestListener struct {
	connections chan net.Conn
	closed      chan struct{}
	closeOnce   sync.Once
}

func newControlTestListener() *controlTestListener {
	return &controlTestListener{
		connections: make(chan net.Conn),
		closed:      make(chan struct{}),
	}
}

func (l *controlTestListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connections:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *controlTestListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (*controlTestListener) Addr() net.Addr {
	return stubAddr("127.0.0.1:7000")
}

type closeAcceptListener struct {
	initial   []net.Conn
	final     net.Conn
	closed    chan struct{}
	closeOnce sync.Once
	next      int
}

func (l *closeAcceptListener) Accept() (net.Conn, error) {
	if l.next < len(l.initial) {
		conn := l.initial[l.next]
		l.next++
		return conn, nil
	}
	if l.next == len(l.initial) {
		<-l.closed
		l.next++
		return l.final, nil
	}
	return nil, net.ErrClosed
}

func (l *closeAcceptListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (*closeAcceptListener) Addr() net.Addr {
	return stubAddr("127.0.0.1:7000")
}

type notifiedAcceptError struct {
	notify chan struct{}
	once   *sync.Once
}

func (e notifiedAcceptError) Error() string {
	if e.notify != nil {
		e.once.Do(func() { close(e.notify) })
	}
	return "accept failed"
}

type repeatedErrorListener struct {
	closed     chan struct{}
	closeOnce  sync.Once
	calls      int
	notifyCall int
	notify     chan struct{}
	notifyOnce sync.Once
}

func (l *repeatedErrorListener) Accept() (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	default:
	}
	l.calls++
	if l.calls == l.notifyCall {
		return nil, notifiedAcceptError{notify: l.notify, once: &l.notifyOnce}
	}
	return nil, notifiedAcceptError{}
}

func (l *repeatedErrorListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (*repeatedErrorListener) Addr() net.Addr {
	return stubAddr("127.0.0.1:7000")
}

func waitForTestEvent(t *testing.T, event <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-event:
	case <-time.After(5 * time.Second):
		t.Fatal(failure)
	}
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

func TestStartControlPlaneShutdownClosesAcceptedConnections(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	cfg = &config.ServerConfig{ControlPort: 7000}
	controlTLSConfig = nil
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
	})

	clientConn, serverConn := net.Pipe()
	trackedConn := newTrackingConn(serverConn)
	listener := newSingleConnListener(trackedConn)
	stop := make(chan struct{})
	t.Cleanup(func() { clientConn.Close() })

	if err := startControlPlane(stop, func(string, string) (net.Listener, error) {
		return listener, nil
	}); err != nil {
		t.Fatalf("startControlPlane: %v", err)
	}

	select {
	case <-trackedConn.deadlineSet:
	case <-time.After(time.Second):
		t.Fatal("accepted connection was not handed to the control handler")
	}
	close(stop)

	select {
	case <-trackedConn.closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not close the accepted control connection")
	}
	select {
	case <-listener.closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not close the control listener")
	}
}

func TestStartControlPlaneRejectsUnsecuredTLSConnection(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	cfg = &config.ServerConfig{ControlPort: 7000, ControlTLSEnabled: true}
	controlTLSConfig = nil
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
	})

	clientConn, serverConn := net.Pipe()
	trackedConn := newTrackingConn(serverConn)
	listener := newSingleConnListener(trackedConn)
	stop := make(chan struct{})
	t.Cleanup(func() { clientConn.Close() })

	if err := startControlPlane(stop, func(string, string) (net.Listener, error) {
		return listener, nil
	}); err != nil {
		t.Fatalf("startControlPlane: %v", err)
	}

	select {
	case <-trackedConn.closed:
	case <-time.After(time.Second):
		t.Fatal("TLS connection was not rejected when security was uninitialized")
	}
	close(stop)
	select {
	case <-listener.closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not close the control listener")
	}
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

func TestRunMainReturnsErrorWhenSecurityInitializationFails(t *testing.T) {
	oldCfg := cfg
	oldNewManager := mainNewCertmagicManager
	oldStartControlPlane := serverStartControlPlane
	oldStartAdminServer := serverStartAdminServer
	oldStartPublicServer := serverStartPublicServer
	t.Cleanup(func() {
		cfg = oldCfg
		mainNewCertmagicManager = oldNewManager
		serverStartControlPlane = oldStartControlPlane
		serverStartAdminServer = oldStartAdminServer
		serverStartPublicServer = oldStartPublicServer
	})

	wantErr := errors.New("manager initialization failed")
	mainNewCertmagicManager = func(context.Context, autocert.Config) (certificateManager, error) {
		return nil, wantErr
	}
	serverStartControlPlane = func(<-chan struct{}, func(string, string) (net.Listener, error)) error {
		t.Fatal("control plane should not start after security initialization failure")
		return nil
	}
	serverStartAdminServer = func(<-chan struct{}, func(string, http.Handler) error) <-chan error {
		t.Fatal("admin server should not start after security initialization failure")
		return nil
	}
	serverStartPublicServer = func(<-chan struct{}, func(string, http.Handler) error, func(string, string, string, http.Handler) error) <-chan error {
		t.Fatal("public server should not start after security initialization failure")
		return nil
	}

	var stdout bytes.Buffer
	code := runMain(&stdout, func() (*config.ServerConfig, error) {
		return &config.ServerConfig{
			BaseDomain:  "tunnel.example.com",
			TLSAuto:     true,
			TLSEmail:    "admin@example.com",
			TLSProvider: "cloudflare",
			TLSAPIToken: "api-token",
		}, nil
	}, nil, nil, nil)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "failed to initialize control security") || !strings.Contains(stdout.String(), wantErr.Error()) {
		t.Fatalf("stdout = %q, want security initialization error", stdout.String())
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
			if w.Code != http.StatusPermanentRedirect {
				t.Fatalf("redirect status = %d, want %d", w.Code, http.StatusPermanentRedirect)
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

func TestHTTPSRedirectUsesConfiguredDomainAndPort(t *testing.T) {
	handler := httpsRedirectHandler(&config.ServerConfig{BaseDomain: "tunnel.example.com", PublicPort: 8443})
	request := httptest.NewRequest(http.MethodGet, "http://attacker.example/path?q=1", nil)
	request.Host = "attacker.example"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d, want 308", response.Code)
	}
	if location := response.Header().Get("Location"); location != "https://tunnel.example.com:8443/path?q=1" {
		t.Fatalf("Location = %q", location)
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
	oldManager := autoTLSManager
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
		autoTLSManager = oldManager
	})

	stop := make(chan struct{})
	called := make(chan struct{})

	autoTLSManager = &fakeCertificateManager{serve: func(handler http.Handler) error {
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
	}}

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
	oldManager := autoTLSManager
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
		autoTLSManager = oldManager
	})

	wantErr := errors.New("certmagic failed")
	autoTLSManager = &fakeCertificateManager{serve: func(http.Handler) error {
		return wantErr
	}}

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

func TestStartPublicServerAutoTLSRequiresInitializedManager(t *testing.T) {
	oldCfg := cfg
	oldManager := autoTLSManager
	cfg = &config.ServerConfig{BaseDomain: "tunnel.example.com", PublicPort: 443, TLSAuto: true}
	autoTLSManager = nil
	t.Cleanup(func() {
		cfg = oldCfg
		autoTLSManager = oldManager
	})

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

	err := <-errs
	if err == nil || err.Error() != "automatic TLS is not initialized" {
		t.Fatalf("err = %v, want automatic TLS initialization error", err)
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

func TestHTTPProxyStripsTunnelCredentialsAndSpoofedForwardingHeaders(t *testing.T) {
	type receivedHeaders struct {
		authorization   string
		forwarded       string
		forwardedFor    string
		forwardedHost   string
		forwardedProto  string
		connectionValue string
		removedValue    string
	}
	received := make(chan receivedHeaders, 1)
	local := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- receivedHeaders{
			authorization:   r.Header.Get("Authorization"),
			forwarded:       r.Header.Get("Forwarded"),
			forwardedFor:    r.Header.Get("X-Forwarded-For"),
			forwardedHost:   r.Header.Get("X-Forwarded-Host"),
			forwardedProto:  r.Header.Get("X-Forwarded-Proto"),
			connectionValue: r.Header.Get("Connection"),
			removedValue:    r.Header.Get("X-Remove-Me"),
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer local.Close()

	setupTunnelWithAuth(t, "secure-headers", local.Listener.Addr().String(), "admin:secret")
	proxyAddr := startProxyServer(t)
	req, err := http.NewRequest(http.MethodGet, "http://"+proxyAddr+"/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = "secure-headers.localhost:8080"
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Forwarded", "for=attacker.example")
	req.Header.Set("Connection", "X-Remove-Me")
	req.Header.Set("X-Remove-Me", "spoofed")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	got := <-received
	if got.authorization != "" {
		t.Fatalf("local service received tunnel Authorization header %q", got.authorization)
	}
	if got.forwardedFor == "203.0.113.10" {
		t.Fatal("local service received spoofed X-Forwarded-For")
	}
	if got.forwardedHost != "secure-headers.localhost:8080" {
		t.Fatalf("X-Forwarded-Host = %q", got.forwardedHost)
	}
	if got.forwardedProto != "http" {
		t.Fatalf("X-Forwarded-Proto = %q", got.forwardedProto)
	}
	if strings.Contains(got.forwarded, "attacker.example") {
		t.Fatalf("Forwarded = %q, contains spoofed value", got.forwarded)
	}
	if got.connectionValue != "" || got.removedValue != "" {
		t.Fatalf("hop-by-hop headers reached local service: Connection=%q X-Remove-Me=%q", got.connectionValue, got.removedValue)
	}
}

func TestHTTPProxyUpgradeBidirectional(t *testing.T) {
	localLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { localLn.Close() })

	go func() {
		conn, acceptErr := localLn.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		request, readErr := http.ReadRequest(reader)
		if readErr != nil {
			return
		}
		request.Body.Close()
		fmt.Fprint(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: ratatosk-test\r\n\r\n")
		payload := make([]byte, 4)
		if _, readErr := io.ReadFull(reader, payload); readErr == nil {
			conn.Write(bytes.ToUpper(payload))
		}
	}()

	setupTunnel(t, "upgrade", localLn.Addr().String())
	proxyAddr := startProxyServer(t)
	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: upgrade.localhost:8080\r\nConnection: Upgrade\r\nUpgrade: ratatosk-test\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", response.StatusCode)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	payload := make([]byte, 4)
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(payload) != "PING" {
		t.Fatalf("payload = %q, want PING", payload)
	}
}

func TestSecureControlConnectionAuthenticatesBeforeYamux(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	oldToken := controlToken
	oldTimeout := serverHandshakeTimeout
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
		controlToken = oldToken
		serverHandshakeTimeout = oldTimeout
	})

	certificate, _, _ := generateTestCertificate(t)
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)

	const expectedToken = "0123456789abcdef0123456789abcdef"
	cfg = &config.ServerConfig{ControlTLSEnabled: true}
	controlTLSConfig = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	controlToken = expectedToken
	serverHandshakeTimeout = time.Second

	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{name: "correct token", token: expectedToken},
		{name: "wrong token", token: "fedcba9876543210fedcba9876543210", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			t.Cleanup(func() {
				clientConn.Close()
				serverConn.Close()
			})

			type secureResult struct {
				conn net.Conn
				err  error
			}
			serverResult := make(chan secureResult, 1)
			applicationData := make(chan []byte, 1)
			go func() {
				secured, secureErr := secureControlConnection(serverConn)
				serverResult <- secureResult{conn: secured, err: secureErr}
				if secureErr != nil {
					return
				}
				payload := make([]byte, len("yamux"))
				if _, readErr := io.ReadFull(secured, payload); readErr == nil {
					applicationData <- payload
				}
			}()

			tlsClient := tls.Client(clientConn, &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    roots,
				ServerName: "localhost",
			})
			clientErr := control.AuthenticateAsClient(tlsClient, tt.token, time.Second)
			result := <-serverResult

			if tt.wantErr {
				if !errors.Is(clientErr, control.ErrAuthenticationFailed) {
					t.Fatalf("client authentication error = %v, want %v", clientErr, control.ErrAuthenticationFailed)
				}
				if !errors.Is(result.err, control.ErrAuthenticationFailed) {
					t.Fatalf("server authentication error = %v, want %v", result.err, control.ErrAuthenticationFailed)
				}
				if result.conn != nil {
					t.Fatal("wrong token returned a connection that could be passed to yamux")
				}
				select {
				case payload := <-applicationData:
					t.Fatalf("application data %q reached the pre-yamux connection", payload)
				default:
				}
				return
			}

			if clientErr != nil {
				t.Fatalf("AuthenticateAsClient: %v", clientErr)
			}
			if result.err != nil {
				t.Fatalf("secureControlConnection: %v", result.err)
			}
			if result.conn == nil {
				t.Fatal("successful authentication returned a nil connection")
			}
			if _, err := tlsClient.Write([]byte("yamux")); err != nil {
				t.Fatalf("write post-authentication data: %v", err)
			}
			select {
			case payload := <-applicationData:
				if string(payload) != "yamux" {
					t.Fatalf("application data = %q, want yamux", payload)
				}
			case <-time.After(time.Second):
				t.Fatal("authenticated connection did not carry post-authentication data")
			}
		})
	}
}

func TestSecureControlConnectionPlaintext(t *testing.T) {
	oldCfg := cfg
	cfg = &config.ServerConfig{}
	t.Cleanup(func() { cfg = oldCfg })

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	securedConn, err := secureControlConnection(serverConn)
	if err != nil {
		t.Fatalf("secureControlConnection: %v", err)
	}
	if securedConn != serverConn {
		t.Fatal("plaintext control connection was replaced")
	}
}

func TestSecureControlConnectionRejectsUninitializedTLS(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	cfg = &config.ServerConfig{ControlTLSEnabled: true}
	controlTLSConfig = nil
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
	})

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	securedConn, err := secureControlConnection(serverConn)
	if err == nil || err.Error() != "control TLS is not initialized" {
		t.Fatalf("err = %v, want uninitialized TLS error", err)
	}
	if securedConn != nil {
		t.Fatal("uninitialized TLS returned a secured connection")
	}
}

func TestSecureControlConnectionDeadlineFailure(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	cfg = &config.ServerConfig{ControlTLSEnabled: true}
	controlTLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
	})

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})
	wantErr := errors.New("deadline failed")

	securedConn, err := secureControlConnection(deadlineErrorConn{Conn: serverConn, err: wantErr})
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "setting TLS handshake deadline") {
		t.Fatalf("err = %v, want wrapped deadline error", err)
	}
	if securedConn != nil {
		t.Fatal("deadline failure returned a secured connection")
	}
}

func TestSecureControlConnectionRejectsInvalidTLSHandshake(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	oldTimeout := serverHandshakeTimeout
	certificate, _, _ := generateTestCertificate(t)
	cfg = &config.ServerConfig{ControlTLSEnabled: true}
	controlTLSConfig = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	serverHandshakeTimeout = time.Second
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
		serverHandshakeTimeout = oldTimeout
	})

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})
	result := make(chan error, 1)
	go func() {
		_, err := secureControlConnection(serverConn)
		result <- err
	}()

	if _, err := io.WriteString(clientConn, "GET / HTTP/1.1\r\n\r\n"); err != nil {
		t.Fatalf("write plaintext handshake: %v", err)
	}
	clientConn.Close()

	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "TLS handshake failed") {
			t.Fatalf("err = %v, want TLS handshake failure", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("invalid TLS handshake was not rejected")
	}
}

func TestHandleConnectionHandshakeTimeout(t *testing.T) {
	oldTimeout := serverHandshakeTimeout
	serverHandshakeTimeout = 20 * time.Millisecond
	t.Cleanup(func() { serverHandshakeTimeout = oldTimeout })

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan struct{})
	go func() {
		handleConnection(serverConn)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleConnection did not enforce its handshake deadline")
	}
}

func TestPrepareServerSecurityRejectsInvalidTLSKeyPair(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	oldToken := controlToken
	oldManager := autoTLSManager
	cfg = &config.ServerConfig{
		ControlTLSEnabled:  true,
		ControlTLSCertFile: "missing-cert.pem",
		ControlTLSKeyFile:  "missing-key.pem",
		ControlToken:       "0123456789abcdef0123456789abcdef",
	}
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
		controlToken = oldToken
		autoTLSManager = oldManager
	})

	err := prepareServerSecurity(context.Background())
	if err == nil {
		t.Fatal("expected invalid control TLS key pair error")
	}
	if !strings.Contains(err.Error(), "loading control TLS key pair") {
		t.Fatalf("error = %q, want key-pair context", err)
	}
}

func TestPrepareServerSecurityClearsStateWhenControlTLSDisabled(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	oldToken := controlToken
	oldManager := autoTLSManager
	cfg = &config.ServerConfig{}
	controlTLSConfig = &tls.Config{MinVersion: tls.VersionTLS13}
	controlToken = "stale-token"
	autoTLSManager = &fakeCertificateManager{}
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
		controlToken = oldToken
		autoTLSManager = oldManager
	})

	if err := prepareServerSecurity(context.Background()); err != nil {
		t.Fatalf("prepareServerSecurity: %v", err)
	}
	if controlTLSConfig != nil || controlToken != "" || autoTLSManager != nil {
		t.Fatalf("security state was not reset: config=%v token=%q manager=%v", controlTLSConfig, controlToken, autoTLSManager)
	}
}

func TestPrepareServerSecurityRejectsInvalidControlConfiguration(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	oldToken := controlToken
	oldManager := autoTLSManager
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
		controlToken = oldToken
		autoTLSManager = oldManager
	})

	tests := []struct {
		name      string
		config    *config.ServerConfig
		wantError string
	}{
		{
			name:      "missing token",
			config:    &config.ServerConfig{ControlTLSEnabled: true},
			wantError: "control token is required",
		},
		{
			name: "missing certificate source",
			config: &config.ServerConfig{
				ControlTLSEnabled: true,
				ControlToken:      "0123456789abcdef0123456789abcdef",
			},
			wantError: "control TLS certificate source is unavailable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg = tt.config
			err := prepareServerSecurity(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("err = %v, want %q", err, tt.wantError)
			}
		})
	}
}

func TestPrepareServerSecurityPrefersDedicatedControlCertificate(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	oldToken := controlToken
	oldManager := autoTLSManager
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
		controlToken = oldToken
		autoTLSManager = oldManager
	})

	dedicatedCertificate, dedicatedCertFile, dedicatedKeyFile := generateTestCertificate(t)
	_, publicCertFile, publicKeyFile := generateTestCertificate(t)
	cfg = &config.ServerConfig{
		ControlTLSEnabled:  true,
		ControlTLSCertFile: dedicatedCertFile,
		ControlTLSKeyFile:  dedicatedKeyFile,
		ControlToken:       "0123456789abcdef0123456789abcdef",
		TLSEnabled:         true,
		TLSCertFile:        publicCertFile,
		TLSKeyFile:         publicKeyFile,
	}

	if err := prepareServerSecurity(context.Background()); err != nil {
		t.Fatalf("prepareServerSecurity: %v", err)
	}
	if controlTLSConfig == nil {
		t.Fatal("control TLS config is nil")
	}
	if len(controlTLSConfig.Certificates) != 1 {
		t.Fatalf("control TLS certificates = %d, want 1", len(controlTLSConfig.Certificates))
	}
	if !bytes.Equal(controlTLSConfig.Certificates[0].Certificate[0], dedicatedCertificate.Certificate[0]) {
		t.Fatal("control TLS did not prefer its dedicated certificate")
	}
	if controlTLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("minimum TLS version = %d, want TLS 1.2", controlTLSConfig.MinVersion)
	}
}

func TestPrepareServerSecurityReusesPublicManualCertificate(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	oldToken := controlToken
	oldManager := autoTLSManager
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
		controlToken = oldToken
		autoTLSManager = oldManager
	})

	publicCertificate, publicCertFile, publicKeyFile := generateTestCertificate(t)
	cfg = &config.ServerConfig{
		ControlTLSEnabled: true,
		ControlToken:      "0123456789abcdef0123456789abcdef",
		TLSEnabled:        true,
		TLSCertFile:       publicCertFile,
		TLSKeyFile:        publicKeyFile,
	}

	if err := prepareServerSecurity(context.Background()); err != nil {
		t.Fatalf("prepareServerSecurity: %v", err)
	}
	if controlTLSConfig == nil {
		t.Fatal("control TLS config is nil")
	}
	if len(controlTLSConfig.Certificates) != 1 {
		t.Fatalf("control TLS certificates = %d, want 1", len(controlTLSConfig.Certificates))
	}
	if !bytes.Equal(controlTLSConfig.Certificates[0].Certificate[0], publicCertificate.Certificate[0]) {
		t.Fatal("control TLS did not reuse the public manual certificate")
	}
}

func TestPrepareServerSecurityReusesAutomaticManager(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	oldToken := controlToken
	oldManager := autoTLSManager
	oldNewManager := mainNewCertmagicManager
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
		controlToken = oldToken
		autoTLSManager = oldManager
		mainNewCertmagicManager = oldNewManager
	})

	managerTLSConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	manager := &fakeCertificateManager{tlsConfig: managerTLSConfig}
	var gotConfig autocert.Config
	mainNewCertmagicManager = func(_ context.Context, managerConfig autocert.Config) (certificateManager, error) {
		gotConfig = managerConfig
		return manager, nil
	}
	cfg = &config.ServerConfig{
		BaseDomain:        "tunnel.example.com",
		ControlTLSEnabled: true,
		ControlToken:      "0123456789abcdef0123456789abcdef",
		TLSAuto:           true,
		TLSEmail:          "admin@example.com",
		TLSProvider:       "cloudflare",
		TLSAPIToken:       "api-token",
	}

	if err := prepareServerSecurity(context.Background()); err != nil {
		t.Fatalf("prepareServerSecurity: %v", err)
	}
	if gotConfig.Email != cfg.TLSEmail || gotConfig.Provider != cfg.TLSProvider || gotConfig.APIToken != cfg.TLSAPIToken {
		t.Fatalf("manager config = %+v, want values from server config", gotConfig)
	}
	wantDomains := []string{"tunnel.example.com", "*.tunnel.example.com"}
	if len(gotConfig.Domains) != len(wantDomains) || gotConfig.Domains[0] != wantDomains[0] || gotConfig.Domains[1] != wantDomains[1] {
		t.Fatalf("manager domains = %v, want %v", gotConfig.Domains, wantDomains)
	}
	if autoTLSManager != manager {
		t.Fatal("automatic TLS manager was not retained for public serving")
	}
	if controlTLSConfig != managerTLSConfig {
		t.Fatal("control TLS did not reuse the automatic manager TLS configuration")
	}
}

func TestPrepareServerSecurityLoadsTokenFile(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	oldToken := controlToken
	oldManager := autoTLSManager
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
		controlToken = oldToken
		autoTLSManager = oldManager
	})

	_, certFile, keyFile := generateTestCertificate(t)
	const wantToken = "0123456789abcdef0123456789abcdef"
	tokenFile := filepath.Join(t.TempDir(), "control-token")
	if err := os.WriteFile(tokenFile, []byte("  \n"+wantToken+"\n  "), 0o600); err != nil {
		t.Fatalf("WriteFile token: %v", err)
	}
	cfg = &config.ServerConfig{
		ControlTLSEnabled:  true,
		ControlTLSCertFile: certFile,
		ControlTLSKeyFile:  keyFile,
		ControlTokenFile:   tokenFile,
	}

	if err := prepareServerSecurity(context.Background()); err != nil {
		t.Fatalf("prepareServerSecurity: %v", err)
	}
	if controlToken != wantToken {
		t.Fatalf("control token = %q, want trimmed token", controlToken)
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

func TestAdminAPITunnelEndpointsUseServerConfig(t *testing.T) {
	oldCfg := cfg
	cfg = &config.ServerConfig{
		BaseDomain: "tunnels.example.test",
		PublicPort: 8443,
		TLSEnabled: true,
	}
	t.Cleanup(func() { cfg = oldCfg })

	connectedAt := time.Date(2026, time.July, 11, 12, 0, 0, 0, time.UTC)
	handler := newAdminHandlerFS(tunnelListStub{
		{Subdomain: "brisk-oak", Protocol: protocol.ProtoHTTP, ConnectedAt: connectedAt},
		{Protocol: protocol.ProtoTCP, PublicPort: 12000, ConnectedAt: connectedAt},
		{Protocol: protocol.ProtoUDP, PublicPort: 12001, ConnectedAt: connectedAt},
	}, fstest.MapFS{}, "dev", noopCheckUpdate, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/tunnels", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var body struct {
		Tunnels []struct {
			Protocol   string  `json:"protocol"`
			Subdomain  *string `json:"subdomain"`
			PublicPort *int    `json:"public_port"`
			Endpoint   string  `json:"endpoint"`
		} `json:"tunnels"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	wantEndpoints := map[string]string{
		protocol.ProtoHTTP: "https://brisk-oak.tunnels.example.test:8443",
		protocol.ProtoTCP:  "tunnels.example.test:12000",
		protocol.ProtoUDP:  "tunnels.example.test:12001",
	}
	if len(body.Tunnels) != len(wantEndpoints) {
		t.Fatalf("tunnels = %d, want %d", len(body.Tunnels), len(wantEndpoints))
	}
	for _, tunnel := range body.Tunnels {
		if tunnel.Endpoint != wantEndpoints[tunnel.Protocol] {
			t.Errorf("%s endpoint = %q, want %q", tunnel.Protocol, tunnel.Endpoint, wantEndpoints[tunnel.Protocol])
		}
		if tunnel.Protocol == protocol.ProtoHTTP {
			if tunnel.Subdomain == nil || *tunnel.Subdomain == "" {
				t.Error("HTTP tunnel is missing subdomain")
			}
			if tunnel.PublicPort != nil {
				t.Error("HTTP tunnel unexpectedly includes public_port")
			}
		} else {
			if tunnel.PublicPort == nil || *tunnel.PublicPort == 0 {
				t.Errorf("%s tunnel is missing public_port", tunnel.Protocol)
			}
			if tunnel.Subdomain != nil {
				t.Errorf("%s tunnel unexpectedly includes subdomain", tunnel.Protocol)
			}
		}
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
	handler := newAdminHandlerFS(registry, brokenFS{}, "dev", noopCheckUpdate, cfg)

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
	}, "dev", noopCheckUpdate, cfg)

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
	}, "dev", noopCheckUpdate, cfg)

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

func TestConstantTimeEqual(t *testing.T) {
	tests := []struct {
		name     string
		actual   string
		expected string
		want     bool
	}{
		{name: "equal", actual: "admin:secret", expected: "admin:secret", want: true},
		{name: "different value", actual: "admin:wrong!", expected: "admin:secret"},
		{name: "different length", actual: "short", expected: "admin:secret"},
		{name: "empty", actual: "", expected: "", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := constantTimeEqual(tt.actual, tt.expected); got != tt.want {
				t.Fatalf("constantTimeEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeCredentialMatchesExpectedLength(t *testing.T) {
	tests := []struct {
		name       string
		credential string
		length     int
		want       []byte
	}{
		{name: "shorter", credential: "abc", length: 5, want: []byte{'a', 'b', 'c', 0, 0}},
		{name: "equal", credential: "abc", length: 3, want: []byte("abc")},
		{name: "longer", credential: "abcdef", length: 3, want: []byte("abc")},
		{name: "empty", credential: "abc", length: 0, want: []byte{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeCredential(tt.credential, tt.length)
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("normalizeCredential() = %v, want %v", got, tt.want)
			}
		})
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
	p := freePort(t)
	portAlloc = tunnel.NewPortAllocator(p, p+1)
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
	if resp.Error != "unable to create tunnel" {
		t.Fatalf("error = %q, want generic tunnel failure", resp.Error)
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
	p := freePort(t)
	portAlloc = tunnel.NewPortAllocator(p, p+1)
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
	if resp.Error != "unable to create tunnel" {
		t.Fatalf("error = %q, want generic tunnel failure", resp.Error)
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
	p := freePort(t)
	portAlloc = tunnel.NewPortAllocator(p, p+1)
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

	if _, ok := registry.GetPortEntry(p); ok {
		t.Fatal("port remained registered after response write failure")
	}
	if got, err := portAlloc.Allocate(); err != nil || got != p {
		t.Fatalf("Allocate() = (%d, %v), want (%d, nil)", got, err, p)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
	if err != nil {
		t.Fatalf("listener was not closed: %v", err)
	}
	ln.Close()
}

func TestHandleUDPTunnelAllocationFailure(t *testing.T) {
	oldAlloc := portAlloc
	p := freePort(t)
	portAlloc = tunnel.NewPortAllocator(p, p+1)
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
	if resp.Error != "unable to create tunnel" {
		t.Fatalf("error = %q, want generic tunnel failure", resp.Error)
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
	p := freePort(t)
	portAlloc = tunnel.NewPortAllocator(p, p+1)
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
	if resp.Error != "unable to create tunnel" {
		t.Fatalf("error = %q, want generic tunnel failure", resp.Error)
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
	p := freePort(t)
	portAlloc = fixedPortAllocator{port: p}
	serverResolveUDPAddr = net.ResolveUDPAddr
	serverListenUDP = net.ListenUDP
	t.Cleanup(func() {
		portAlloc = oldAlloc
		serverResolveUDPAddr = oldResolveUDPAddr
		serverListenUDP = oldListenUDP
	})

	busy, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: p})
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
	if resp.Error != "unable to create tunnel" {
		t.Fatalf("error = %q, want generic tunnel failure", resp.Error)
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
	p := freePort(t)
	portAlloc = tunnel.NewPortAllocator(p, p+1)
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

	if _, ok := registry.GetPortEntry(p); ok {
		t.Fatal("port remained registered after response write failure")
	}
	if got, err := portAlloc.Allocate(); err != nil || got != p {
		t.Fatalf("Allocate() = (%d, %v), want (%d, nil)", got, err, p)
	}

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: p})
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
	}, "v1.0.0", func(string) string { return "" }, cfg)

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

func TestAdminHandlerRequiresConfiguredBasicAuth(t *testing.T) {
	serverConfig := &config.ServerConfig{AdminUsername: "admin", AdminPassword: "secret"}
	handler := newAdminHandlerFS(registry, fstest.MapFS{
		"dashboard/dist/index.html": {Data: []byte("<html></html>")},
	}, "dev", noopCheckUpdate, serverConfig)

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/tunnels", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/tunnels", nil)
	request.SetBasicAuth("admin", "secret")
	authorized := httptest.NewRecorder()
	handler.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want 200", authorized.Code)
	}
}

func TestAdminAPIVersionUpdateAvailable(t *testing.T) {
	handler := newAdminHandlerFS(registry, fstest.MapFS{
		"dashboard/dist/index.html": {Data: []byte("<html></html>")},
	}, "v1.0.0", func(string) string { return "v2.0.0" }, cfg)

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
	}, cfg)

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
	}, "v1.0.0", noopCheckUpdate, cfg)

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

func TestRunMainReturnsZeroOnShutdownSignal(t *testing.T) {
	oldCfg := cfg
	oldCheckUpdate := serverCheckUpdate
	oldStartControlPlane := serverStartControlPlane
	oldStartAdminServer := serverStartAdminServer
	oldStartPublicServer := serverStartPublicServer
	t.Cleanup(func() {
		cfg = oldCfg
		serverCheckUpdate = oldCheckUpdate
		serverStartControlPlane = oldStartControlPlane
		serverStartAdminServer = oldStartAdminServer
		serverStartPublicServer = oldStartPublicServer
	})

	serverCheckUpdate = noopCheckUpdate
	started := make(chan struct{}, 3)
	stopped := make(chan struct{})
	serverStartControlPlane = func(stop <-chan struct{}, _ func(string, string) (net.Listener, error)) error {
		started <- struct{}{}
		go func() {
			<-stop
			close(stopped)
		}()
		return nil
	}
	serverStartAdminServer = func(<-chan struct{}, func(string, http.Handler) error) <-chan error {
		started <- struct{}{}
		return make(chan error)
	}
	serverStartPublicServer = func(<-chan struct{}, func(string, http.Handler) error, func(string, string, string, http.Handler) error) <-chan error {
		started <- struct{}{}
		return make(chan error)
	}

	type result struct {
		code int
		log  string
	}
	resultCh := make(chan result, 1)
	go func() {
		var output bytes.Buffer
		code := runMain(&output, func() (*config.ServerConfig, error) {
			return &config.ServerConfig{
				BaseDomain:     "localhost",
				PublicPort:     8080,
				AdminPort:      8081,
				ControlPort:    7000,
				PortRangeStart: 34000,
				PortRangeEnd:   34010,
			}, nil
		}, nil, nil, nil)
		resultCh <- result{code: code, log: output.String()}
	}()

	for range 3 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("runMain did not start all server components")
		}
	}
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	if err := process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal process: %v", err)
	}

	select {
	case got := <-resultCh:
		if got.code != 0 {
			t.Fatalf("code = %d, want 0", got.code)
		}
		if !strings.Contains(got.log, "shutdown signal received") {
			t.Fatalf("log = %q, want shutdown signal message", got.log)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runMain did not return after interrupt")
	}
	waitForTestEvent(t, stopped, "runMain did not close the control-plane stop channel")
}

func TestDefaultAutomaticTLSManagerRejectsUnsupportedProvider(t *testing.T) {
	_, err := mainNewCertmagicManager(context.Background(), autocert.Config{
		Provider: "unsupported",
		Domains:  []string{"tunnel.example.com", "*.tunnel.example.com"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported DNS provider") {
		t.Fatalf("err = %v, want unsupported provider error", err)
	}
}

func TestStartControlPlaneCapsAcceptBackoffAndStopsRetry(t *testing.T) {
	oldCfg := cfg
	oldLogger := slog.Default()
	cfg = &config.ServerConfig{ControlPort: 7000}
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		cfg = oldCfg
		slog.SetDefault(oldLogger)
	})

	listener := &repeatedErrorListener{
		closed:     make(chan struct{}),
		notifyCall: 9,
		notify:     make(chan struct{}),
	}
	stop := make(chan struct{})
	var stopOnce sync.Once
	stopServer := func() { stopOnce.Do(func() { close(stop) }) }
	t.Cleanup(func() {
		stopServer()
		listener.Close()
	})

	if err := startControlPlane(stop, func(string, string) (net.Listener, error) {
		return listener, nil
	}); err != nil {
		t.Fatalf("startControlPlane: %v", err)
	}
	waitForTestEvent(t, listener.notify, "accept loop did not reach the capped backoff")
	stopServer()
	waitForTestEvent(t, listener.closed, "control listener was not closed during backoff")
}

func TestStartControlPlaneRejectsHandshakeAndConnectionOverflow(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	cfg = &config.ServerConfig{ControlPort: 7000, ControlTLSEnabled: true}
	controlTLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
	})

	listener := newControlTestListener()
	stop := make(chan struct{})
	var stopOnce sync.Once
	stopServer := func() { stopOnce.Do(func() { close(stop) }) }
	remoteRelease := make(chan struct{})
	var releaseOnce sync.Once
	releaseRemotes := func() { releaseOnce.Do(func() { close(remoteRelease) }) }
	allConnections := make([]*controlTestConn, 0, maxControlConnections+1)
	t.Cleanup(func() {
		releaseRemotes()
		stopServer()
		listener.Close()
		for _, conn := range allConnections {
			conn.Close()
		}
	})

	if err := startControlPlane(stop, func(string, string) (net.Listener, error) {
		return listener, nil
	}); err != nil {
		t.Fatalf("startControlPlane: %v", err)
	}

	for range maxPendingControlHandshakes {
		conn := newControlTestConn(nil)
		allConnections = append(allConnections, conn)
		listener.connections <- conn
		waitForTestEvent(t, conn.readCalled, "connection did not occupy a handshake slot")
	}

	handshakeRejected := make([]*controlTestConn, 0, maxControlConnections-maxPendingControlHandshakes)
	for range maxControlConnections - maxPendingControlHandshakes {
		conn := newControlTestConn(remoteRelease)
		allConnections = append(allConnections, conn)
		handshakeRejected = append(handshakeRejected, conn)
		listener.connections <- conn
		waitForTestEvent(t, conn.remoteCalled, "handshake overflow was not rejected")
	}

	connectionRejected := newControlTestConn(nil)
	allConnections = append(allConnections, connectionRejected)
	listener.connections <- connectionRejected
	waitForTestEvent(t, connectionRejected.remoteCalled, "connection overflow was not rejected")
	waitForTestEvent(t, connectionRejected.closed, "connection overflow was not closed")

	releaseRemotes()
	for _, conn := range handshakeRejected {
		waitForTestEvent(t, conn.closed, "handshake overflow connection was not closed")
	}
	stopServer()
	waitForTestEvent(t, listener.closed, "control listener was not closed")
}

func TestStartControlPlaneStopsConnectionBeforeHandshakeAdmission(t *testing.T) {
	oldCfg := cfg
	oldTLSConfig := controlTLSConfig
	cfg = &config.ServerConfig{ControlPort: 7000, ControlTLSEnabled: true}
	controlTLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	t.Cleanup(func() {
		cfg = oldCfg
		controlTLSConfig = oldTLSConfig
	})

	initial := make([]net.Conn, 0, maxPendingControlHandshakes)
	blockers := make([]*controlTestConn, 0, maxPendingControlHandshakes)
	for range maxPendingControlHandshakes {
		conn := newControlTestConn(nil)
		initial = append(initial, conn)
		blockers = append(blockers, conn)
	}
	final := newControlTestConn(nil)
	listener := &closeAcceptListener{
		initial: initial,
		final:   final,
		closed:  make(chan struct{}),
	}
	stop := make(chan struct{})
	var stopOnce sync.Once
	stopServer := func() { stopOnce.Do(func() { close(stop) }) }
	t.Cleanup(func() {
		stopServer()
		listener.Close()
		final.Close()
		for _, conn := range blockers {
			conn.Close()
		}
	})

	if err := startControlPlane(stop, func(string, string) (net.Listener, error) {
		return listener, nil
	}); err != nil {
		t.Fatalf("startControlPlane: %v", err)
	}
	for _, conn := range blockers {
		waitForTestEvent(t, conn.readCalled, "connection did not occupy a handshake slot")
	}

	stopServer()
	waitForTestEvent(t, final.closed, "connection accepted during shutdown was not closed")
	for _, conn := range blockers {
		waitForTestEvent(t, conn.closed, "active handshake was not closed during shutdown")
	}
}

func TestHandleConnectionLogsDeadlineClearFailure(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	wantErr := errors.New("clear deadline failed")
	sequencedConn := &deadlineSequenceConn{Conn: serverConn, failAt: 2, failErr: wantErr}
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	done := make(chan struct{})
	go func() {
		handleConnection(sequencedConn)
		close(done)
	}()
	clientSession, err := tunnel.NewClientSession(clientConn)
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	controlStream, err := clientSession.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := protocol.WriteRequest(controlStream, &protocol.TunnelRequest{Protocol: "unsupported"}); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}
	response, err := protocol.ReadResponse(controlStream)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if response.Success || response.Error != "unsupported protocol" {
		t.Fatalf("response = %+v, want unsupported protocol failure", response)
	}
	clientSession.Close()
	waitForTestEvent(t, done, "handleConnection did not return")

	sequencedConn.mu.Lock()
	calls := sequencedConn.calls
	sequencedConn.mu.Unlock()
	if calls != 2 {
		t.Fatalf("SetDeadline calls = %d, want 2", calls)
	}
}
