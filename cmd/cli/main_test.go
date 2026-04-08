package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"ratatosk/internal/inspector"
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

func TestRunClientAbruptDisconnect(t *testing.T) {
	addr := startMockRelay(t, func(conn net.Conn) {
		session, _ := tunnel.NewServerSession(conn)

		cs, _ := session.Accept()
		protocol.ReadRequest(cs)
		protocol.WriteResponse(cs, &protocol.TunnelResponse{Success: true, Subdomain: "test-abrupt"})
		cs.Close()

		// Let the client enter the Accept loop, then kill the connection
		// without a graceful yamux shutdown to trigger the non-EOF path.
		time.Sleep(100 * time.Millisecond)
		conn.Close()
	})

	if err := runClient(addr, 13002); err != nil {
		t.Fatalf("runClient: %v", err)
	}
}

func TestRunClientServerClosesEarly(t *testing.T) {
	addr := startMockRelay(t, func(conn net.Conn) {
		session, _ := tunnel.NewServerSession(conn)
		// Close immediately without accepting any streams.
		session.Close()
		conn.Close()
	})

	err := runClient(addr, 3000)
	if err == nil {
		t.Fatal("expected error when server closes session before handshake")
	}
}

func TestRunClientReadResponseError(t *testing.T) {
	addr := startMockRelay(t, func(conn net.Conn) {
		session, _ := tunnel.NewServerSession(conn)
		cs, _ := session.Accept()
		protocol.ReadRequest(cs)
		// Close without writing a response.
		cs.Close()
		session.Close()
		conn.Close()
	})

	err := runClient(addr, 3000)
	if err == nil {
		t.Fatal("expected error when server closes without responding")
	}
}

func TestRunClientWithTraffic(t *testing.T) {
	// Local HTTP server that the client proxies to.
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer local.Close()
	_, portStr, _ := net.SplitHostPort(local.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	addr := startMockRelay(t, func(conn net.Conn) {
		defer conn.Close()
		session, _ := tunnel.NewServerSession(conn)
		defer session.Close()

		cs, _ := session.Accept()
		protocol.ReadRequest(cs)
		protocol.WriteResponse(cs, &protocol.TunnelResponse{Success: true, Subdomain: "test-traffic"})
		cs.Close()

		// Simulate an incoming HTTP request through the tunnel.
		time.Sleep(100 * time.Millisecond)
		stream, err := session.Open()
		if err != nil {
			return
		}
		req, _ := http.NewRequest("GET", "http://localhost/test", nil)
		req.Write(stream)
		http.ReadResponse(bufio.NewReader(stream), req)
		stream.Close()

		time.Sleep(100 * time.Millisecond)
	})

	if err := runClient(addr, port); err != nil {
		t.Fatalf("runClient: %v", err)
	}
}

func TestHandleStream(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer local.Close()

	clientConn, serverConn := net.Pipe()
	logger := inspector.NewLogger()

	go handleStream(serverConn, local.Listener.Addr().String(), logger)

	fmt.Fprintf(clientConn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")

	resp, err := http.ReadResponse(bufio.NewReader(clientConn), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	resp.Body.Close()
	clientConn.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
