package tunnel

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type acceptErrorListener struct {
	closed chan struct{}
	err    error
	mu     sync.Mutex
	sent   bool
}

func newAcceptErrorListener(err error) *acceptErrorListener {
	return &acceptErrorListener{
		closed: make(chan struct{}),
		err:    err,
	}
}

func (l *acceptErrorListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if !l.sent {
		l.sent = true
		l.mu.Unlock()
		return nil, l.err
	}
	l.mu.Unlock()

	<-l.closed
	return nil, net.ErrClosed
}

func (l *acceptErrorListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *acceptErrorListener) Addr() net.Addr {
	return stubAddr("127.0.0.1:0")
}

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

func TestServeTCPAcceptError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	listener := newAcceptErrorListener(errors.New("accept failed"))

	done := make(chan struct{})
	go func() {
		ServeTCP(ctx, listener, nil)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		listener.mu.Lock()
		sent := listener.sent
		listener.mu.Unlock()
		if sent {
			cancel()
			listener.Close()
			select {
			case <-done:
				return
			case <-time.After(2 * time.Second):
				t.Fatal("ServeTCP did not stop after cancel")
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	listener.Close()
	t.Fatal("ServeTCP did not observe accept error")
}

func TestProxyTCPConnOpenError(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	serverSession, err := NewServerSession(serverConn)
	if err != nil {
		t.Fatalf("NewServerSession: %v", err)
	}
	clientSession, err := NewClientSession(clientConn)
	if err != nil {
		t.Fatalf("NewClientSession: %v", err)
	}

	clientSession.Close()
	serverSession.Close()

	publicConn, publicRemote := net.Pipe()
	defer publicConn.Close()

	done := make(chan struct{})
	go func() {
		proxyTCPConn(publicRemote, serverSession)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("proxyTCPConn did not return when session open failed")
	}
}
