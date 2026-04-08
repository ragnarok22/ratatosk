package tunnel

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// newTestSession creates a yamux server session backed by a net.Pipe.
// Both the pipe ends and the session must be closed by the caller.
func newTestSession(t *testing.T) (clientConn net.Conn, session *yamux.Session) {
	t.Helper()
	c, s := net.Pipe()
	sess, err := NewServerSession(s)
	if err != nil {
		c.Close()
		s.Close()
		t.Fatalf("NewServerSession: %v", err)
	}
	t.Cleanup(func() {
		sess.Close()
		c.Close()
		s.Close()
	})
	return c, sess
}

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if r.sessions == nil {
		t.Fatal("sessions map is nil")
	}
}

func TestRegisterAndGetSession(t *testing.T) {
	r := NewRegistry()
	_, session := newTestSession(t)

	r.Register("abc123", session, "")

	got, ok := r.GetSession("abc123")
	if !ok {
		t.Fatal("GetSession returned false for registered subdomain")
	}
	if got != session {
		t.Fatal("GetSession returned a different session")
	}
}

func TestGetSessionNotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.GetSession("nonexistent")
	if ok {
		t.Fatal("GetSession returned true for unregistered subdomain")
	}
}

func TestUnregister(t *testing.T) {
	r := NewRegistry()
	_, session := newTestSession(t)

	r.Register("abc123", session, "")
	r.Unregister("abc123")

	_, ok := r.GetSession("abc123")
	if ok {
		t.Fatal("GetSession returned true after Unregister")
	}
}

func TestUnregisterNonexistent(t *testing.T) {
	r := NewRegistry()
	// Should not panic.
	r.Unregister("nope")
}

func TestRegisterOverwrite(t *testing.T) {
	r := NewRegistry()
	_, sess1 := newTestSession(t)
	_, sess2 := newTestSession(t)

	r.Register("sub", sess1, "")
	r.Register("sub", sess2, "")

	got, ok := r.GetSession("sub")
	if !ok {
		t.Fatal("GetSession returned false")
	}
	if got != sess2 {
		t.Fatal("Register did not overwrite existing session")
	}
}

func TestHasSubdomain(t *testing.T) {
	r := NewRegistry()
	_, session := newTestSession(t)

	if r.HasSubdomain("abc123") {
		t.Fatal("HasSubdomain returned true before registration")
	}

	r.Register("abc123", session, "")
	if !r.HasSubdomain("abc123") {
		t.Fatal("HasSubdomain returned false after registration")
	}

	r.Unregister("abc123")
	if r.HasSubdomain("abc123") {
		t.Fatal("HasSubdomain returned true after unregistration")
	}
}

func TestListTunnels(t *testing.T) {
	r := NewRegistry()
	_, sess1 := newTestSession(t)
	_, sess2 := newTestSession(t)

	r.Register("alpha", sess1, "")
	r.Register("beta", sess2, "")

	tunnels := r.ListTunnels()
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(tunnels))
	}

	names := map[string]bool{}
	for _, ti := range tunnels {
		names[ti.Subdomain] = true
		if ti.ConnectedAt.IsZero() {
			t.Fatalf("ConnectedAt is zero for %s", ti.Subdomain)
		}
	}
	if !names["alpha"] || !names["beta"] {
		t.Fatalf("missing expected subdomains: got %v", names)
	}
}

func TestListTunnelsEmpty(t *testing.T) {
	r := NewRegistry()
	tunnels := r.ListTunnels()
	if len(tunnels) != 0 {
		t.Fatalf("expected 0 tunnels, got %d", len(tunnels))
	}
}

func TestGetEntry(t *testing.T) {
	r := NewRegistry()
	_, session := newTestSession(t)

	r.Register("abc123", session, "")

	entry, ok := r.GetEntry("abc123")
	if !ok {
		t.Fatal("GetEntry returned false for registered subdomain")
	}
	if entry.Session != session {
		t.Fatal("GetEntry returned a different session")
	}
	if entry.BasicAuth != "" {
		t.Errorf("BasicAuth = %q, want empty", entry.BasicAuth)
	}
}

func TestGetEntryNotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.GetEntry("nonexistent")
	if ok {
		t.Fatal("GetEntry returned true for unregistered subdomain")
	}
}

func TestRegisterWithBasicAuth(t *testing.T) {
	r := NewRegistry()
	_, session := newTestSession(t)

	r.Register("secure", session, "admin:secret")

	entry, ok := r.GetEntry("secure")
	if !ok {
		t.Fatal("GetEntry returned false for registered subdomain")
	}
	if entry.BasicAuth != "admin:secret" {
		t.Errorf("BasicAuth = %q, want %q", entry.BasicAuth, "admin:secret")
	}
}

func TestRegisterPort(t *testing.T) {
	r := NewRegistry()
	_, session := newTestSession(t)

	entry := &TunnelEntry{
		Session:     session,
		ConnectedAt: time.Now(),
		Protocol:    "tcp",
		PublicPort:  12345,
	}
	r.RegisterPort(12345, entry)

	got, ok := r.GetPortEntry(12345)
	if !ok {
		t.Fatal("GetPortEntry returned false for registered port")
	}
	if got.Protocol != "tcp" {
		t.Errorf("Protocol = %q, want %q", got.Protocol, "tcp")
	}
}

func TestUnregisterPort(t *testing.T) {
	r := NewRegistry()
	_, session := newTestSession(t)

	entry := &TunnelEntry{
		Session:     session,
		ConnectedAt: time.Now(),
		Protocol:    "tcp",
		PublicPort:  12345,
	}
	r.RegisterPort(12345, entry)
	r.UnregisterPort(12345)

	_, ok := r.GetPortEntry(12345)
	if ok {
		t.Fatal("GetPortEntry returned true after UnregisterPort")
	}
}

func TestGetPortEntryNotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.GetPortEntry(99999)
	if ok {
		t.Fatal("GetPortEntry returned true for unregistered port")
	}
}

func TestListTunnelsMixed(t *testing.T) {
	r := NewRegistry()
	_, sess1 := newTestSession(t)
	_, sess2 := newTestSession(t)

	r.Register("alpha", sess1, "")
	r.RegisterPort(12345, &TunnelEntry{
		Session:     sess2,
		ConnectedAt: time.Now(),
		Protocol:    "tcp",
		PublicPort:  12345,
	})

	tunnels := r.ListTunnels()
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(tunnels))
	}

	var hasSub, hasPort bool
	for _, ti := range tunnels {
		if ti.Subdomain == "alpha" {
			hasSub = true
		}
		if ti.PublicPort == 12345 && ti.Protocol == "tcp" {
			hasPort = true
		}
	}
	if !hasSub {
		t.Error("missing HTTP tunnel in ListTunnels")
	}
	if !hasPort {
		t.Error("missing TCP tunnel in ListTunnels")
	}
}

func TestConcurrentPortAccess(t *testing.T) {
	r := NewRegistry()

	const n = 50
	sessions := make([]*yamux.Session, n)
	for i := range sessions {
		_, sess := newTestSession(t)
		sessions[i] = sess
	}

	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.RegisterPort(40000+i, &TunnelEntry{
				Session:     sessions[i],
				ConnectedAt: time.Now(),
				Protocol:    "tcp",
				PublicPort:  40000 + i,
			})
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = r.GetPortEntry(40000 + i)
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.UnregisterPort(40000 + i)
		}(i)
	}
	wg.Wait()
}

func TestConcurrentAccess(t *testing.T) {
	r := NewRegistry()

	const n = 50
	sessions := make([]*yamux.Session, n)
	for i := range sessions {
		_, sess := newTestSession(t)
		sessions[i] = sess
	}

	var wg sync.WaitGroup

	// Concurrent registers.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.Register(fmt.Sprintf("sub-%d", i), sessions[i], "")
		}(i)
	}
	wg.Wait()

	// Concurrent reads.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = r.GetSession(fmt.Sprintf("sub-%d", i))
		}(i)
	}
	wg.Wait()

	// Concurrent unregisters.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.Unregister(fmt.Sprintf("sub-%d", i))
		}(i)
	}
	wg.Wait()
}
