package main

import (
	"net"
	"testing"
	"time"

	"ratatosk/internal/protocol"
	"ratatosk/internal/tunnel"
)

func startMockRelay(t *testing.T, handler func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		handler(conn)
	}()
	return ln.Addr().String()
}

func TestRunClientHappyPath(t *testing.T) {
	addr := startMockRelay(t, func(conn net.Conn) {
		defer conn.Close()
		session, err := tunnel.NewServerSession(conn)
		if err != nil {
			return
		}
		defer session.Close()

		cs, err := session.Accept()
		if err != nil {
			return
		}
		if _, err := protocol.ReadRequest(cs); err != nil {
			cs.Close()
			return
		}
		protocol.WriteResponse(cs, &protocol.TunnelResponse{
			Success:   true,
			Subdomain: "test-happy",
		})
		cs.Close()
		// Server session close triggers EOF on client Accept loop.
	})

	if err := runClient(addr, 13001); err != nil {
		t.Fatalf("runClient: %v", err)
	}
}

func TestRunClientConnectionRefused(t *testing.T) {
	// Bind a port and immediately close to guarantee nothing is listening.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	if err := runClient(addr, 3000); err == nil {
		t.Fatal("expected connection error")
	}
}

func TestRunClientHandshakeFailure(t *testing.T) {
	addr := startMockRelay(t, func(conn net.Conn) {
		defer conn.Close()
		session, err := tunnel.NewServerSession(conn)
		if err != nil {
			return
		}
		defer session.Close()

		cs, err := session.Accept()
		if err != nil {
			return
		}
		protocol.ReadRequest(cs)
		protocol.WriteResponse(cs, &protocol.TunnelResponse{
			Success: false,
			Error:   "test rejection",
		})
		cs.Close()
	})

	err := runClient(addr, 3000)
	if err == nil {
		t.Fatal("expected error for rejected handshake")
	}
}

func TestRunClientBadServer(t *testing.T) {
	addr := startMockRelay(t, func(conn net.Conn) {
		conn.Write([]byte("garbage"))
		conn.Close()
	})

	// Allow a moment for the listener to be ready.
	time.Sleep(10 * time.Millisecond)

	err := runClient(addr, 3000)
	if err == nil {
		t.Fatal("expected error for bad server data")
	}
}
