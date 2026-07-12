package certmagic

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
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

func TestNewManagerConfiguresACMEAndManagesDomains(t *testing.T) {
	oldManageSync := manageSyncFunc
	t.Cleanup(func() { manageSyncFunc = oldManageSync })

	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "value")
	domains := []string{"example.com", "*.example.com"}
	var gotConfig *certmagic.Config
	manageSyncFunc = func(cfg *certmagic.Config, gotCtx context.Context, gotDomains []string) error {
		gotConfig = cfg
		if gotCtx != ctx {
			t.Errorf("ManageSync context differs from NewManager context")
		}
		if !slices.Equal(gotDomains, domains) {
			t.Errorf("ManageSync domains = %v, want %v", gotDomains, domains)
		}
		return nil
	}

	manager, err := NewManager(ctx, Config{
		Email:    "test@example.com",
		Provider: "cloudflare",
		APIToken: "cf-token",
		Domains:  domains,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(manager.cache.Stop)

	if manager.config != gotConfig {
		t.Fatal("manager does not retain the managed CertMagic config")
	}
	if len(gotConfig.Issuers) != 1 {
		t.Fatalf("issuers = %d, want 1", len(gotConfig.Issuers))
	}
	issuer, ok := gotConfig.Issuers[0].(*certmagic.ACMEIssuer)
	if !ok {
		t.Fatalf("issuer type = %T, want *certmagic.ACMEIssuer", gotConfig.Issuers[0])
	}
	if issuer.Email != "test@example.com" || !issuer.Agreed {
		t.Errorf("ACME issuer email/agreed = %q/%t", issuer.Email, issuer.Agreed)
	}
	if issuer.DNS01Solver == nil {
		t.Fatal("ACME DNS01Solver should not be nil")
	}
}

func TestNewManagerReturnsManageSyncError(t *testing.T) {
	oldManageSync := manageSyncFunc
	t.Cleanup(func() { manageSyncFunc = oldManageSync })

	wantErr := errors.New("ACME unavailable")
	manageSyncFunc = func(*certmagic.Config, context.Context, []string) error {
		return wantErr
	}

	manager, err := NewManager(context.Background(), Config{
		Email:    "test@example.com",
		Provider: "cloudflare",
		APIToken: "cf-token",
		Domains:  []string{"example.com"},
	})
	if manager != nil {
		t.Fatal("manager should be nil when ManageSync fails")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped %v", err, wantErr)
	}
}

func TestManagerTLSConfigIsReusable(t *testing.T) {
	manager := newTestManager(t)

	first := manager.TLSConfig()
	second := manager.TLSConfig()
	if first == second {
		t.Fatal("TLSConfig returned the same config pointer")
	}
	if first.GetCertificate == nil || second.GetCertificate == nil {
		t.Fatal("TLSConfig must use CertMagic GetCertificate")
	}
	if first.MinVersion < tls.VersionTLS12 {
		t.Fatalf("MinVersion = %d, want TLS 1.2 or newer", first.MinVersion)
	}
	if len(first.NextProtos) == 0 || len(first.CipherSuites) == 0 || len(first.CurvePreferences) == 0 {
		t.Fatal("TLSConfig is missing modern CertMagic defaults")
	}

	first.MinVersion = tls.VersionTLS10
	first.NextProtos[0] = "changed"
	first.CipherSuites[0] = tls.TLS_RSA_WITH_AES_128_CBC_SHA
	first.CurvePreferences[0] = tls.CurveP384
	third := manager.TLSConfig()
	if third.MinVersion == first.MinVersion || third.NextProtos[0] == "changed" {
		t.Fatal("mutating one TLS config affected a later config")
	}
	if third.CipherSuites[0] == first.CipherSuites[0] || third.CurvePreferences[0] == first.CurvePreferences[0] {
		t.Fatal("mutating TLS config slices affected a later config")
	}
}

func TestManagerServeStartsHTTPSAndRedirect(t *testing.T) {
	manager := newTestManager(t)
	oldListen := listenFunc
	oldServeHTTP := serveHTTPFunc
	oldServeHTTPS := serveHTTPSFunc
	t.Cleanup(func() {
		listenFunc = oldListen
		serveHTTPFunc = oldServeHTTP
		serveHTTPSFunc = oldServeHTTPS
	})

	listeners := map[string]*testListener{
		":443": newTestListener(":443"),
		":80":  newTestListener(":80"),
	}
	var listenMu sync.Mutex
	var gotAddresses []string
	listenFunc = func(network, address string) (net.Listener, error) {
		if network != "tcp" {
			t.Errorf("listen network = %q, want tcp", network)
		}
		listenMu.Lock()
		gotAddresses = append(gotAddresses, address)
		listenMu.Unlock()
		return listeners[address], nil
	}

	httpServers := make(chan *http.Server, 1)
	httpsServers := make(chan *http.Server, 1)
	serveHTTPFunc = func(server *http.Server, listener net.Listener) error {
		httpServers <- server
		<-listener.(*testListener).closed
		return http.ErrServerClosed
	}
	wantErr := errors.New("HTTPS stopped")
	serveHTTPSFunc = func(server *http.Server, listener net.Listener) error {
		httpsServers <- server
		return wantErr
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	if err := manager.Serve(handler); !errors.Is(err, wantErr) {
		t.Fatalf("Serve error = %v, want %v", err, wantErr)
	}
	if !slices.Equal(gotAddresses, []string{":443", ":80"}) {
		t.Errorf("listen addresses = %v, want [:443 :80]", gotAddresses)
	}

	httpServer := <-httpServers
	redirectRequest := httptest.NewRequest(http.MethodGet, "http://example.com:80/path?q=1", nil)
	redirectResponse := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(redirectResponse, redirectRequest)
	if redirectResponse.Code != http.StatusMovedPermanently {
		t.Errorf("redirect status = %d, want %d", redirectResponse.Code, http.StatusMovedPermanently)
	}
	if location := redirectResponse.Header().Get("Location"); location != "https://example.com/path?q=1" {
		t.Errorf("redirect location = %q, want %q", location, "https://example.com/path?q=1")
	}

	redirectRequest = httptest.NewRequest(http.MethodGet, "http://attacker.example/path", nil)
	redirectRequest.Host = "attacker.example"
	redirectResponse = httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(redirectResponse, redirectRequest)
	if location := redirectResponse.Header().Get("Location"); location != "https://example.com/path" {
		t.Errorf("untrusted host redirect location = %q, want %q", location, "https://example.com/path")
	}

	httpsServer := <-httpsServers
	response := httptest.NewRecorder()
	httpsServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://example.com/", nil))
	if response.Code != http.StatusNoContent {
		t.Errorf("HTTPS handler status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if httpsServer.TLSConfig.GetCertificate == nil {
		t.Fatal("HTTPS server TLS config is not backed by CertMagic")
	}
	if !slices.Contains(httpsServer.TLSConfig.NextProtos, "h2") || !slices.Contains(httpsServer.TLSConfig.NextProtos, "http/1.1") {
		t.Errorf("HTTPS protocols = %v, want h2 and http/1.1", httpsServer.TLSConfig.NextProtos)
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
		t.Fatalf("error = %q, want mention of unsupported provider", err.Error())
	}
}

func TestSetupAndServeDelegatesToManager(t *testing.T) {
	oldManageSync := manageSyncFunc
	oldListen := listenFunc
	oldServeHTTP := serveHTTPFunc
	oldServeHTTPS := serveHTTPSFunc
	t.Cleanup(func() {
		manageSyncFunc = oldManageSync
		listenFunc = oldListen
		serveHTTPFunc = oldServeHTTP
		serveHTTPSFunc = oldServeHTTPS
	})

	managed := false
	manageSyncFunc = func(*certmagic.Config, context.Context, []string) error {
		managed = true
		return nil
	}
	listeners := map[string]*testListener{
		":443": newTestListener(":443"),
		":80":  newTestListener(":80"),
	}
	listenFunc = func(_ string, address string) (net.Listener, error) {
		return listeners[address], nil
	}
	serveHTTPFunc = func(_ *http.Server, listener net.Listener) error {
		<-listener.(*testListener).closed
		return http.ErrServerClosed
	}
	wantErr := errors.New("HTTPS stopped")
	serveHTTPSFunc = func(*http.Server, net.Listener) error { return wantErr }

	err := SetupAndServe(Config{
		Email:    "test@example.com",
		Provider: "cloudflare",
		APIToken: "token",
		Domains:  []string{"example.com"},
	}, http.NotFoundHandler())
	if !managed {
		t.Fatal("SetupAndServe did not construct and manage certificates")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("SetupAndServe error = %v, want %v", err, wantErr)
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	oldManageSync := manageSyncFunc
	manageSyncFunc = func(*certmagic.Config, context.Context, []string) error { return nil }
	t.Cleanup(func() { manageSyncFunc = oldManageSync })

	manager, err := NewManager(context.Background(), Config{
		Email:    "test@example.com",
		Provider: "cloudflare",
		APIToken: "cf-token",
		Domains:  []string{"example.com"},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(manager.cache.Stop)
	return manager
}

type testListener struct {
	address string
	closed  chan struct{}
	once    sync.Once
}

func newTestListener(address string) *testListener {
	return &testListener{address: address, closed: make(chan struct{})}
}

func (l *testListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *testListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *testListener) Addr() net.Addr { return testAddress(l.address) }

type testAddress string

func (a testAddress) Network() string { return "tcp" }
func (a testAddress) String() string  { return string(a) }
