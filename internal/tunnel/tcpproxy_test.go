package tunnel

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestProxyTCPConnBidirectional(t *testing.T) {
	// Create a yamux session pair over net.Pipe.
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	serverSession, err := NewServerSession(serverConn)
	if err != nil {
		t.Fatalf("NewServerSession: %v", err)
	}
	defer serverSession.Close()

	clientSession, err := NewClientSession(clientConn)
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	defer clientSession.Close()

	// Simulate the public-side connection with a pipe.
	publicConn, publicRemote := net.Pipe()
	defer publicConn.Close()
	defer publicRemote.Close()

	// proxyTCPConn opens a stream on the server session.
	// The client session accepts that stream and acts as the "local service".
	go proxyTCPConn(publicRemote, serverSession)

	// Client side: accept the stream and echo everything back.
	stream, err := clientSession.Accept()
	if err != nil {
		t.Fatalf("client Accept: %v", err)
	}
	go func() {
		io.Copy(stream, stream)
		stream.Close()
	}()

	// Write from the public side, expect echo back.
	msg := []byte("hello TCP tunnel")
	if _, err := publicConn.Write(msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, len(msg))
	publicConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(publicConn, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(buf) != string(msg) {
		t.Errorf("got %q, want %q", buf, msg)
	}
}

func TestServeTCPAcceptLoop(t *testing.T) {
	// Create yamux session pair.
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	serverSession, err := NewServerSession(serverConn)
	if err != nil {
		t.Fatalf("NewServerSession: %v", err)
	}
	defer serverSession.Close()

	clientSession, err := NewClientSession(clientConn)
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}
	defer clientSession.Close()

	// Start a local TCP listener for ServeTCP.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ServeTCP(ctx, ln, serverSession)

	// Client side: accept streams and echo.
	go func() {
		for {
			stream, err := clientSession.Accept()
			if err != nil {
				return
			}
			go func() {
				io.Copy(stream, stream)
				stream.Close()
			}()
		}
	}()

	// Dial the listener as a "public" client.
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	msg := []byte("integration test")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, len(msg))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(buf) != string(msg) {
		t.Errorf("got %q, want %q", buf, msg)
	}

	cancel()
	ln.Close()
}
