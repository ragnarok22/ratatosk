package tunnel

import (
	"context"
	"io"
	"log/slog"
	"net"

	"github.com/hashicorp/yamux"
)

// ServeTCP accepts connections on ln and proxies each one through a new
// yamux stream to the remote client. It blocks until ctx is canceled or
// the listener is closed.
func ServeTCP(ctx context.Context, ln net.Listener, session *yamux.Session) {
	for {
		conn, err := ln.Accept()
		if err != nil {
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

	done := make(chan struct{})
	go func() {
		io.Copy(stream, public)
		close(done)
	}()
	io.Copy(public, stream)
	<-done
}
