package tunnel

import (
	"io"
	"sort"
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
func (r *Registry) Register(subdomain string, session *yamux.Session, basicAuth string, protocol string) {
	r.mu.Lock()
	r.sessions[subdomain] = newTunnelEntry(session, basicAuth, protocol)
	r.mu.Unlock()
}

// RegisterIfAbsent atomically registers a subdomain unless it is already in use.
func (r *Registry) RegisterIfAbsent(subdomain string, session *yamux.Session, basicAuth string, protocol string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[subdomain]; ok {
		return false
	}
	r.sessions[subdomain] = newTunnelEntry(session, basicAuth, protocol)
	return true
}

func newTunnelEntry(session *yamux.Session, basicAuth string, protocol string) *TunnelEntry {
	return &TunnelEntry{
		Session:     session,
		ConnectedAt: time.Now(),
		BasicAuth:   basicAuth,
		Protocol:    protocol,
	}
}

// Unregister removes a subdomain from the registry.
func (r *Registry) Unregister(subdomain string) {
	r.mu.Lock()
	delete(r.sessions, subdomain)
	r.mu.Unlock()
}

// UnregisterIfSession removes a subdomain only when it still belongs to session.
func (r *Registry) UnregisterIfSession(subdomain string, session *yamux.Session) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.sessions[subdomain]
	if !ok || entry.Session != session {
		return false
	}
	delete(r.sessions, subdomain)
	return true
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
	if !ok {
		r.mu.RUnlock()
		return nil, false
	}
	snapshot := *entry
	r.mu.RUnlock()
	return &snapshot, true
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

// RegisterPortIfAbsent atomically registers a port unless it is already in use.
func (r *Registry) RegisterPortIfAbsent(port int, entry *TunnelEntry) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.ports[port]; ok {
		return false
	}
	r.ports[port] = entry
	return true
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

// UnregisterPortIfEntry removes and closes a port only when entry is current.
func (r *Registry) UnregisterPortIfEntry(port int, entry *TunnelEntry) bool {
	r.mu.Lock()
	current, ok := r.ports[port]
	if !ok || current != entry {
		r.mu.Unlock()
		return false
	}
	delete(r.ports, port)
	r.mu.Unlock()
	if entry.Listener != nil {
		entry.Listener.Close()
	}
	return true
}

// GetPortEntry returns the tunnel entry for a public port, if it exists.
func (r *Registry) GetPortEntry(port int) (*TunnelEntry, bool) {
	r.mu.RLock()
	entry, ok := r.ports[port]
	if !ok {
		r.mu.RUnlock()
		return nil, false
	}
	snapshot := *entry
	r.mu.RUnlock()
	return &snapshot, true
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
	sort.Slice(tunnels, func(i, j int) bool {
		if tunnels[i].Protocol != tunnels[j].Protocol {
			return tunnels[i].Protocol < tunnels[j].Protocol
		}
		if tunnels[i].Subdomain != tunnels[j].Subdomain {
			return tunnels[i].Subdomain < tunnels[j].Subdomain
		}
		return tunnels[i].PublicPort < tunnels[j].PublicPort
	})
	return tunnels
}
