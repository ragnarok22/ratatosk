package tunnel

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestServeUDPRoundTrip(t *testing.T) {
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

	// Server-side UDP socket.
	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ResolveUDPAddr: %v", err)
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer udpConn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ServeUDP(ctx, udpConn, serverSession)

	// Client side: accept a stream, read framed datagrams, and echo them back.
	go func() {
		for {
			stream, err := clientSession.Accept()
			if err != nil {
				return
			}
			go func() {
				defer stream.Close()
				for {
					data, err := ReadFrame(stream)
					if err != nil {
						return
					}
					if err := WriteFrame(stream, data); err != nil {
						return
					}
				}
			}()
		}
	}()

	// Send a UDP datagram to the server socket.
	clientUDP, err := net.DialUDP("udp", nil, udpConn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer clientUDP.Close()

	msg := []byte("hello UDP tunnel")
	if _, err := clientUDP.Write(msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read the echo response.
	buf := make([]byte, MaxUDPFrameSize)
	clientUDP.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := clientUDP.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != string(msg) {
		t.Errorf("got %q, want %q", buf[:n], msg)
	}
}

func TestServeUDPMultipleClients(t *testing.T) {
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

	udpAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer udpConn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ServeUDP(ctx, udpConn, serverSession)

	// Client side: echo frames back.
	go func() {
		for {
			stream, err := clientSession.Accept()
			if err != nil {
				return
			}
			go func() {
				defer stream.Close()
				for {
					data, err := ReadFrame(stream)
					if err != nil {
						return
					}
					WriteFrame(stream, data)
				}
			}()
		}
	}()

	// Two separate UDP "clients" sending to the same server.
	for i, msg := range []string{"client-A", "client-B"} {
		c, err := net.DialUDP("udp", nil, udpConn.LocalAddr().(*net.UDPAddr))
		if err != nil {
			t.Fatalf("DialUDP[%d]: %v", i, err)
		}
		defer c.Close()

		if _, err := c.Write([]byte(msg)); err != nil {
			t.Fatalf("Write[%d]: %v", i, err)
		}

		buf := make([]byte, MaxUDPFrameSize)
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := c.Read(buf)
		if err != nil {
			t.Fatalf("Read[%d]: %v", i, err)
		}
		if string(buf[:n]) != msg {
			t.Errorf("[%d] got %q, want %q", i, buf[:n], msg)
		}
	}
}
