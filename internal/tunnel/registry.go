package tunnel

import (
	"io"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// TunnelEntry holds a yamux session and metadata for a registered tunnel.
type TunnelEntry struct {
	Session     *yamux.Session
	ConnectedAt time.Time
	BasicAuth   string
	Protocol    string
	LocalPort   int
	PublicPort  int
	Listener    io.Closer
}

// TunnelInfo is the exported DTO returned by ListTunnels.
type TunnelInfo struct {
	Subdomain   string    `json:"subdomain,omitempty"`
	Protocol    string    `json:"protocol"`
	PublicPort  int       `json:"public_port,omitempty"`
	ConnectedAt time.Time `json:"connected_at"`
}

// Registry is a thread-safe map of subdomains and ports to active tunnel entries.
type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*TunnelEntry
	ports    map[int]*TunnelEntry
}

// NewRegistry creates an empty tunnel registry.
func NewRegistry() *Registry {
	return &Registry{
		sessions: make(map[string]*TunnelEntry),
		ports:    make(map[int]*TunnelEntry),
	}
}

// Register associates a subdomain with a yamux session.
func (r *Registry) Register(subdomain string, session *yamux.Session, basicAuth string) {
	r.mu.Lock()
	r.sessions[subdomain] = &TunnelEntry{
		Session:     session,
		ConnectedAt: time.Now(),
		BasicAuth:   basicAuth,
	}
	r.mu.Unlock()
}

// Unregister removes a subdomain from the registry.
func (r *Registry) Unregister(subdomain string) {
	r.mu.Lock()
	delete(r.sessions, subdomain)
	r.mu.Unlock()
}

// GetSession returns the yamux session for a subdomain, if it exists.
func (r *Registry) GetSession(subdomain string) (*yamux.Session, bool) {
	r.mu.RLock()
	entry, ok := r.sessions[subdomain]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return entry.Session, true
}

// GetEntry returns the full tunnel entry for a subdomain, if it exists.
func (r *Registry) GetEntry(subdomain string) (*TunnelEntry, bool) {
	r.mu.RLock()
	entry, ok := r.sessions[subdomain]
	r.mu.RUnlock()
	return entry, ok
}

// HasSubdomain reports whether a subdomain is already registered.
func (r *Registry) HasSubdomain(subdomain string) bool {
	r.mu.RLock()
	_, ok := r.sessions[subdomain]
	r.mu.RUnlock()
	return ok
}

// RegisterPort associates a public port with a tunnel entry (TCP/UDP tunnels).
func (r *Registry) RegisterPort(port int, entry *TunnelEntry) {
	r.mu.Lock()
	r.ports[port] = entry
	r.mu.Unlock()
}

// UnregisterPort removes a port-based tunnel and closes its listener if set.
func (r *Registry) UnregisterPort(port int) {
	r.mu.Lock()
	entry, ok := r.ports[port]
	delete(r.ports, port)
	r.mu.Unlock()
	if ok && entry.Listener != nil {
		entry.Listener.Close()
	}
}

// GetPortEntry returns the tunnel entry for a public port, if it exists.
func (r *Registry) GetPortEntry(port int) (*TunnelEntry, bool) {
	r.mu.RLock()
	entry, ok := r.ports[port]
	r.mu.RUnlock()
	return entry, ok
}

// ListTunnels returns info about all active tunnels (HTTP + TCP/UDP).
func (r *Registry) ListTunnels() []TunnelInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tunnels := make([]TunnelInfo, 0, len(r.sessions)+len(r.ports))
	for sub, entry := range r.sessions {
		tunnels = append(tunnels, TunnelInfo{
			Subdomain:   sub,
			Protocol:    entry.Protocol,
			ConnectedAt: entry.ConnectedAt,
		})
	}
	for _, entry := range r.ports {
		tunnels = append(tunnels, TunnelInfo{
			Protocol:    entry.Protocol,
			PublicPort:  entry.PublicPort,
			ConnectedAt: entry.ConnectedAt,
		})
	}
	return tunnels
}
