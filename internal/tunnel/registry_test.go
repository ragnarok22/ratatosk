package tunnel

import (
	"fmt"
	"net"
	"sync"
	"testing"

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

	r.Register("abc123", session)

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

	r.Register("abc123", session)
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

	r.Register("sub", sess1)
	r.Register("sub", sess2)

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

	r.Register("abc123", session)
	if !r.HasSubdomain("abc123") {
		t.Fatal("HasSubdomain returned false after registration")
	}

	r.Unregister("abc123")
	if r.HasSubdomain("abc123") {
		t.Fatal("HasSubdomain returned true after unregistration")
	}
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
			r.Register(fmt.Sprintf("sub-%d", i), sessions[i])
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
