package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"

	"ratatosk/internal/tunnel"
)

type tunnelsResponse struct {
	Tunnels []tunnel.TunnelInfo `json:"tunnels"`
}

func newAdminHandler(reg tunnelLister) http.Handler {
	return newAdminHandlerFS(reg, dashboardFS)
}

func newAdminHandlerFS(reg tunnelLister, dashboard fs.FS) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/tunnels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tunnelsResponse{Tunnels: reg.ListTunnels()})
	})

	// Serve the embedded SPA, or a placeholder if the dist was not built.
	sub, err := fs.Sub(dashboard, "dashboard/dist")
	if err != nil {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("Dashboard not built. Run: make build"))
		})
		return mux
	}

	// Check if the embedded FS actually has content.
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("Dashboard not built. Run: make build"))
		})
		return mux
	}

	mux.Handle("GET /", spaHandler(sub))
	return mux
}

// spaHandler serves static files from fsys, falling back to index.html
// for paths that don't match a real file (client-side routing support).
func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
