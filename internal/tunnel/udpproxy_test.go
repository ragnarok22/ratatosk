package tunnel

import (
	"context"
	"errors"
	"io"
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
					if err := WriteFrame(stream, data); err != nil {
						return
					}
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

func TestServeUDPSessionOpenError(t *testing.T) {
	oldWriteFrame := udpWriteFrame
	oldReadFrame := udpReadFrame
	udpWriteFrame = WriteFrame
	udpReadFrame = ReadFrame
	t.Cleanup(func() {
		udpWriteFrame = oldWriteFrame
		udpReadFrame = oldReadFrame
	})

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

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer udpConn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		ServeUDP(ctx, udpConn, serverSession)
		close(done)
	}()

	clientUDP, err := net.DialUDP("udp", nil, udpConn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer clientUDP.Close()

	if _, err := clientUDP.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	cancel()
	udpConn.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeUDP did not stop after cancel")
	}
}

func TestServeUDPWriteFrameErrorCleansUpPeer(t *testing.T) {
	oldWriteFrame := udpWriteFrame
	oldReadFrame := udpReadFrame
	t.Cleanup(func() {
		udpWriteFrame = oldWriteFrame
		udpReadFrame = oldReadFrame
	})

	udpWriteFrame = func(io.Writer, []byte) error {
		return errors.New("write failed")
	}
	udpReadFrame = ReadFrame

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

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer udpConn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	accepted := make(chan struct{}, 2)
	go func() {
		for {
			stream, err := clientSession.Accept()
			if err != nil {
				return
			}
			closeStream := stream
			accepted <- struct{}{}
			go closeStream.Close()
		}
	}()

	done := make(chan struct{})
	go func() {
		ServeUDP(ctx, udpConn, serverSession)
		close(done)
	}()

	clientUDP, err := net.DialUDP("udp", nil, udpConn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer clientUDP.Close()

	for i := 0; i < 2; i++ {
		if _, err := clientUDP.Write([]byte("hello")); err != nil {
			t.Fatalf("Write[%d]: %v", i, err)
		}
		select {
		case <-accepted:
		case <-time.After(2 * time.Second):
			t.Fatalf("stream %d was not opened", i+1)
		}
	}

	cancel()
	udpConn.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeUDP did not stop after cancel")
	}
}

func TestUDPStreamToConnCanceledContextRemovesPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stream, peer := net.Pipe()
	defer peer.Close()

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer udpConn.Close()

	pm := newPeerManager()
	pm.peers["peer"] = &udpPeer{stream: stream, lastSeen: time.Now()}

	done := make(chan struct{})
	go func() {
		udpStreamToConn(ctx, stream, udpConn, udpConn.LocalAddr().(*net.UDPAddr), "peer", pm)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("udpStreamToConn did not return on canceled context")
	}

	pm.mu.Lock()
	_, ok := pm.peers["peer"]
	pm.mu.Unlock()
	if ok {
		t.Fatal("peer was not removed after canceled context")
	}
}

func TestUDPStreamToConnWriteErrorRemovesPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, peer := net.Pipe()
	defer peer.Close()

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	remoteAddr := udpConn.LocalAddr().(*net.UDPAddr)
	udpConn.Close()

	pm := newPeerManager()
	pm.peers["peer"] = &udpPeer{stream: stream, lastSeen: time.Now()}

	done := make(chan struct{})
	go func() {
		udpStreamToConn(ctx, stream, udpConn, remoteAddr, "peer", pm)
		close(done)
	}()

	if err := WriteFrame(peer, []byte("hello")); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("udpStreamToConn did not return after UDP write error")
	}

	pm.mu.Lock()
	_, ok := pm.peers["peer"]
	pm.mu.Unlock()
	if ok {
		t.Fatal("peer was not removed after UDP write error")
	}
}
