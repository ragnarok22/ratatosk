package tunnel

import (
	"sync"

	"github.com/hashicorp/yamux"
)

// Registry is a thread-safe map of subdomains to active yamux sessions.
type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*yamux.Session
}

// NewRegistry creates an empty tunnel registry.
func NewRegistry() *Registry {
	return &Registry{
		sessions: make(map[string]*yamux.Session),
	}
}

// Register associates a subdomain with a yamux session.
func (r *Registry) Register(subdomain string, session *yamux.Session) {
	r.mu.Lock()
	r.sessions[subdomain] = session
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
	session, ok := r.sessions[subdomain]
	r.mu.RUnlock()
	return session, ok
}
