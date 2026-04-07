package tunnel

import (
	"net"
	"testing"
	"time"
)

func TestNewConfig(t *testing.T) {
	cfg := newConfig()

	if !cfg.EnableKeepAlive {
		t.Error("KeepAlive should be enabled")
	}
	if cfg.KeepAliveInterval != 30*time.Second {
		t.Errorf("KeepAliveInterval = %v, want 30s", cfg.KeepAliveInterval)
	}
	if cfg.ConnectionWriteTimeout != 10*time.Second {
		t.Errorf("ConnectionWriteTimeout = %v, want 10s", cfg.ConnectionWriteTimeout)
	}
	if cfg.StreamCloseTimeout != 5*time.Minute {
		t.Errorf("StreamCloseTimeout = %v, want 5m", cfg.StreamCloseTimeout)
	}
	if cfg.StreamOpenTimeout != 75*time.Second {
		t.Errorf("StreamOpenTimeout = %v, want 75s", cfg.StreamOpenTimeout)
	}
}

func TestNewServerSession(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	session, err := NewServerSession(server)
	if err != nil {
		t.Fatalf("NewServerSession: %v", err)
	}
	defer session.Close()

	if session.IsClosed() {
		t.Error("session is closed immediately after creation")
	}
}

func TestNewClientSession(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	session, err := NewClientSession(client)
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	defer session.Close()

	if session.IsClosed() {
		t.Error("session is closed immediately after creation")
	}
}

func TestClientServerStream(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	serverSess, err := NewServerSession(serverConn)
	if err != nil {
		t.Fatalf("NewServerSession: %v", err)
	}
	defer serverSess.Close()

	clientSess, err := NewClientSession(clientConn)
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	defer clientSess.Close()

	// Server opens a stream, client accepts it, data flows both ways.
	errCh := make(chan error, 1)
	go func() {
		stream, err := clientSess.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer stream.Close()

		buf := make([]byte, 5)
		if _, err := stream.Read(buf); err != nil {
			errCh <- err
			return
		}
		if string(buf) != "hello" {
			errCh <- err
			return
		}
		if _, err := stream.Write([]byte("world")); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	stream, err := serverSess.Open()
	if err != nil {
		t.Fatalf("server Open: %v", err)
	}
	defer stream.Close()

	if _, err := stream.Write([]byte("hello")); err != nil {
		t.Fatalf("server Write: %v", err)
	}

	buf := make([]byte, 5)
	if _, err := stream.Read(buf); err != nil {
		t.Fatalf("server Read: %v", err)
	}
	if string(buf) != "world" {
		t.Fatalf("got %q, want %q", string(buf), "world")
	}

	if err := <-errCh; err != nil {
		t.Fatalf("client goroutine: %v", err)
	}
}
