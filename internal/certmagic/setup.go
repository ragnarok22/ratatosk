package certmagic

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
)

var (
	manageSyncFunc = func(cfg *certmagic.Config, ctx context.Context, domains []string) error {
		return cfg.ManageSync(ctx, domains)
	}
	listenFunc     = net.Listen
	serveHTTPFunc  = func(server *http.Server, listener net.Listener) error { return server.Serve(listener) }
	serveHTTPSFunc = func(server *http.Server, listener net.Listener) error {
		return server.ServeTLS(listener, "", "")
	}
)

// Config holds the parameters needed for automatic TLS provisioning.
type Config struct {
	Email    string
	Provider string
	APIToken string
	Domains  []string
}

// Manager provisions certificates and supplies TLS configurations backed by
// CertMagic's in-memory certificate cache.
type Manager struct {
	config     *certmagic.Config
	cache      *certmagic.Cache
	tlsConfig  *tls.Config
	ctx        context.Context
	baseDomain string
}

// NewDNSProvider returns the libdns DNS provider for the given name.
func NewDNSProvider(provider, apiToken string) (certmagic.DNSProvider, error) {
	switch provider {
	case "cloudflare":
		return &cloudflare.Provider{APIToken: apiToken}, nil
	default:
		return nil, fmt.Errorf("unsupported DNS provider: %q (supported: cloudflare)", provider)
	}
}

// NewManager configures DNS-01 certificate management and synchronously
// provisions every configured domain before returning.
func NewManager(ctx context.Context, cfg Config) (*Manager, error) {
	dnsProvider, err := NewDNSProvider(cfg.Provider, cfg.APIToken)
	if err != nil {
		return nil, err
	}

	var magicConfig *certmagic.Config
	cache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) {
			if magicConfig == nil {
				return nil, errors.New("certificate manager is not initialized")
			}
			return magicConfig, nil
		},
	})

	magicConfig = certmagic.New(cache, certmagic.Config{})
	magicConfig.Issuers = []certmagic.Issuer{
		certmagic.NewACMEIssuer(magicConfig, certmagic.ACMEIssuer{
			Email:  cfg.Email,
			Agreed: true,
			DNS01Solver: &certmagic.DNS01Solver{
				DNSManager: certmagic.DNSManager{DNSProvider: dnsProvider},
			},
		}),
	}

	slog.Info("certmagic: provisioning certificates",
		"domains", cfg.Domains,
		"email", cfg.Email,
		"provider", cfg.Provider,
	)

	if err := manageSyncFunc(magicConfig, ctx, cfg.Domains); err != nil {
		cache.Stop()
		return nil, fmt.Errorf("manage certificates: %w", err)
	}

	baseDomain := ""
	for _, domain := range cfg.Domains {
		if !strings.HasPrefix(domain, "*.") {
			baseDomain = domain
			break
		}
	}
	return &Manager{
		config:     magicConfig,
		cache:      cache,
		tlsConfig:  magicConfig.TLSConfig(),
		ctx:        ctx,
		baseDomain: baseDomain,
	}, nil
}

// TLSConfig returns an independent modern TLS configuration backed by
// CertMagic's GetCertificate callback.
func (m *Manager) TLSConfig() *tls.Config {
	config := m.tlsConfig.Clone()
	config.NextProtos = append([]string(nil), m.tlsConfig.NextProtos...)
	config.CipherSuites = append([]uint16(nil), m.tlsConfig.CipherSuites...)
	config.CurvePreferences = append([]tls.CurveID(nil), m.tlsConfig.CurvePreferences...)
	return config
}

// Serve starts HTTPS on :443 and an HTTP-to-HTTPS redirect on :80. It blocks
// until either server exits, then closes the other server.
func (m *Manager) Serve(handler http.Handler) error {
	httpsListener, err := listenFunc("tcp", ":443")
	if err != nil {
		return fmt.Errorf("listen on :443: %w", err)
	}

	httpListener, err := listenFunc("tcp", ":80")
	if err != nil {
		_ = httpsListener.Close()
		return fmt.Errorf("listen on :80: %w", err)
	}

	redirectHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := m.baseDomain
		requestHost := r.Host
		if hostname, _, splitErr := net.SplitHostPort(r.Host); splitErr == nil {
			requestHost = hostname
		}
		if requestHost == m.baseDomain || strings.HasSuffix(requestHost, "."+m.baseDomain) {
			host = requestHost
		}
		w.Header().Set("Connection", "close")
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})

	httpServer := &http.Server{
		Handler:           redirectHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return m.ctx },
	}
	httpsConfig := m.TLSConfig()
	httpsConfig.NextProtos = append([]string{"h2", "http/1.1"}, httpsConfig.NextProtos...)
	httpsServer := &http.Server{
		Handler:           handler,
		TLSConfig:         httpsConfig,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       5 * time.Minute,
		BaseContext:       func(net.Listener) context.Context { return m.ctx },
	}

	errCh := make(chan error, 2)
	go func() { errCh <- serveHTTPFunc(httpServer, httpListener) }()
	go func() { errCh <- serveHTTPSFunc(httpsServer, httpsListener) }()

	firstErr := <-errCh
	_ = httpServer.Close()
	_ = httpsServer.Close()
	_ = httpListener.Close()
	_ = httpsListener.Close()
	secondErr := <-errCh

	if firstErr != nil && !errors.Is(firstErr, http.ErrServerClosed) {
		return firstErr
	}
	if secondErr != nil && !errors.Is(secondErr, http.ErrServerClosed) {
		return secondErr
	}
	return firstErr
}

// SetupAndServe is a compatibility convenience that constructs a Manager and
// serves its HTTP endpoints.
func SetupAndServe(cfg Config, handler http.Handler) error {
	manager, err := NewManager(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer manager.cache.Stop()
	return manager.Serve(handler)
}
