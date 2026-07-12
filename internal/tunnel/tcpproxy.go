package tunnel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/hashicorp/yamux"
)

const tcpHalfCloseDrainTimeout = 30 * time.Second

// ServeTCP accepts connections on ln and proxies each one through a new
// yamux stream to the remote client. It blocks until ctx is canceled or
// the listener is closed.
func ServeTCP(ctx context.Context, ln net.Listener, session *yamux.Session) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
			slog.Error("tcp accept error", "error", err)
			continue
		}
		go proxyTCPConn(conn, session)
	}
}

// proxyTCPConn opens a yamux stream and copies bytes bidirectionally
// between the public connection and the stream.
func proxyTCPConn(public net.Conn, session *yamux.Session) {
	defer public.Close()

	stream, err := session.Open()
	if err != nil {
		slog.Error("failed to open yamux stream for TCP proxy", "error", err)
		return
	}
	defer stream.Close()

	ProxyConnections(public, stream)
}

// ProxyConnections copies bytes in both directions while preserving TCP and
// yamux half-closes. Connections without CloseWrite support are fully closed
// when either direction ends so the other copy cannot leak indefinitely.
func ProxyConnections(left, right net.Conn) {
	type copyResult struct {
		halfClosed       bool
		sourceHalfClosed bool
		err              error
	}
	done := make(chan copyResult, 2)
	copyDirection := func(dst, src net.Conn) {
		_, err := io.Copy(dst, src)
		result := copyResult{err: err, sourceHalfClosed: supportsHalfClose(src)}
		if err == nil && result.sourceHalfClosed {
			result.err = closeWrite(dst)
			result.halfClosed = result.err == nil
		}
		done <- result
	}

	go copyDirection(left, right)
	go copyDirection(right, left)

	first := <-done
	if first.err != nil || !first.halfClosed || !first.sourceHalfClosed {
		interruptConnection(left)
		interruptConnection(right)
		<-done
		return
	}

	timer := time.NewTimer(tcpHalfCloseDrainTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		interruptConnection(left)
		interruptConnection(right)
		<-done
	}
}

func interruptConnection(conn net.Conn) {
	_ = conn.SetDeadline(time.Now())
	_ = conn.Close()
}

func supportsHalfClose(conn net.Conn) bool {
	if conn.LocalAddr() != nil && conn.LocalAddr().Network() == "pipe" {
		return false
	}
	if _, ok := conn.(interface{ CloseWrite() error }); ok {
		return true
	}
	_, ok := conn.(*yamux.Stream)
	return ok
}

func closeWrite(conn net.Conn) error {
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		return closer.CloseWrite()
	}
	if stream, ok := conn.(*yamux.Stream); ok {
		// yamux Stream.Close sends FIN while continuing to permit reads.
		return stream.Close()
	}
	return errors.New("connection does not support half-close")
}
