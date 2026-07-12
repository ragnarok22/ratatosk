package main

import (
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ratatosk/internal/config"
	"ratatosk/internal/protocol"
	"ratatosk/internal/tunnel"
)

type tunnelsResponse struct {
	Tunnels []adminTunnel `json:"tunnels"`
}

type adminTunnel struct {
	Protocol    string    `json:"protocol"`
	Subdomain   string    `json:"subdomain,omitempty"`
	PublicPort  int       `json:"public_port,omitempty"`
	Endpoint    string    `json:"endpoint"`
	ConnectedAt time.Time `json:"connected_at"`
}

type versionResponse struct {
	Version       string `json:"version"`
	LatestVersion string `json:"latest_version,omitempty"`
	UpdateAvail   bool   `json:"update_available"`
}

func newAdminHandler(reg tunnelLister) http.Handler {
	return newAdminHandlerFS(reg, dashboardFS, Version, serverCheckUpdate, cfg)
}

func newAdminHandlerFS(reg tunnelLister, dashboard fs.FS, version string, checkUpdate func(string) string, serverConfig *config.ServerConfig) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/tunnels", func(w http.ResponseWriter, r *http.Request) {
		listed := reg.ListTunnels()
		tunnels := make([]adminTunnel, 0, len(listed))
		for _, info := range listed {
			tunnels = append(tunnels, adminTunnel{
				Protocol:    info.Protocol,
				Subdomain:   info.Subdomain,
				PublicPort:  info.PublicPort,
				Endpoint:    tunnelEndpoint(serverConfig, info),
				ConnectedAt: info.ConnectedAt,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tunnelsResponse{Tunnels: tunnels})
	})

	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		latest := checkUpdate(version)
		resp := versionResponse{
			Version:       version,
			LatestVersion: latest,
			UpdateAvail:   latest != "",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// Serve the embedded SPA, or a placeholder if the dist was not built.
	sub, err := fs.Sub(dashboard, "dashboard/dist")
	if err != nil {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("Dashboard not built. Run: make build"))
		})
		return adminAuth(mux, serverConfig)
	}

	// Check if the embedded FS actually has content.
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("Dashboard not built. Run: make build"))
		})
		return adminAuth(mux, serverConfig)
	}

	mux.Handle("GET /", spaHandler(sub))
	return adminAuth(mux, serverConfig)
}

func adminAuth(next http.Handler, serverConfig *config.ServerConfig) http.Handler {
	next = adminSecurityHeaders(next)
	if serverConfig.AdminUsername == "" {
		return next
	}
	expected := serverConfig.AdminUsername + ":" + serverConfig.AdminPassword
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || !constantTimeEqual(username+":"+password, expected) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Ratatosk Admin"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func adminSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func constantTimeEqual(actual, expected string) bool {
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func tunnelEndpoint(serverConfig *config.ServerConfig, info tunnel.TunnelInfo) string {
	if info.Protocol == protocol.ProtoHTTP {
		return serverConfig.TunnelURL(info.Subdomain)
	}
	return net.JoinHostPort(serverConfig.BaseDomain, strconv.Itoa(info.PublicPort))
}

// spaHandler serves static files from fsys, falling back to index.html
// for paths that don't match a real file (client-side routing support).
func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		// Serve root directly.
		if r.URL.Path == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Try to open the requested file; fall back to index.html.
		_, err := fs.Stat(fsys, strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
