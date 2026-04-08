package certmagic

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
)

// Config holds the parameters needed for automatic TLS provisioning.
type Config struct {
	Email    string
	Provider string
	APIToken string
	Domains  []string
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

// SetupAndServe configures certmagic for automatic ACME DNS-01 wildcard
// certificate provisioning and starts an HTTPS server on :443 with an
// HTTP->HTTPS redirect on :80. It blocks until an error occurs.
func SetupAndServe(cfg Config, handler http.Handler) error {
	dnsProvider, err := NewDNSProvider(cfg.Provider, cfg.APIToken)
	if err != nil {
		return err
	}

	certmagic.DefaultACME.Email = cfg.Email
	certmagic.DefaultACME.Agreed = true
	certmagic.DefaultACME.DNS01Solver = &certmagic.DNS01Solver{
		DNSManager: certmagic.DNSManager{
			DNSProvider: dnsProvider,
		},
	}

	slog.Info("certmagic: provisioning certificates",
		"domains", cfg.Domains,
		"email", cfg.Email,
		"provider", cfg.Provider,
	)

	return certmagic.HTTPS(cfg.Domains, handler)
}
