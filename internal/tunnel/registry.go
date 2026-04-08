package tunnel

import (
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// TunnelEntry holds a yamux session and metadata for a registered tunnel.
type TunnelEntry struct {
	Session     *yamux.Session
	ConnectedAt time.Time
}

// TunnelInfo is the exported DTO returned by ListTunnels.
type TunnelInfo struct {
	Subdomain   string    `json:"subdomain"`
	ConnectedAt time.Time `json:"connected_at"`
}

// Registry is a thread-safe map of subdomains to active tunnel entries.
type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*TunnelEntry
}

// NewRegistry creates an empty tunnel registry.
func NewRegistry() *Registry {
	return &Registry{
		sessions: make(map[string]*TunnelEntry),
	}
}

// Register associates a subdomain with a yamux session.
func (r *Registry) Register(subdomain string, session *yamux.Session) {
	r.mu.Lock()
	r.sessions[subdomain] = &TunnelEntry{
		Session:     session,
		ConnectedAt: time.Now(),
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

// HasSubdomain reports whether a subdomain is already registered.
func (r *Registry) HasSubdomain(subdomain string) bool {
	r.mu.RLock()
	_, ok := r.sessions[subdomain]
	r.mu.RUnlock()
	return ok
}

// ListTunnels returns info about all active tunnels.
func (r *Registry) ListTunnels() []TunnelInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tunnels := make([]TunnelInfo, 0, len(r.sessions))
	for sub, entry := range r.sessions {
		tunnels = append(tunnels, TunnelInfo{
			Subdomain:   sub,
			ConnectedAt: entry.ConnectedAt,
		})
	}
	return tunnels
}
