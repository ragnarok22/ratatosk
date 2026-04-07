package tunnel

import (
	"net"
	"time"

	"github.com/hashicorp/yamux"
)

func newConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 30 * time.Second
	cfg.ConnectionWriteTimeout = 10 * time.Second
	cfg.StreamCloseTimeout = 5 * time.Minute
	cfg.StreamOpenTimeout = 75 * time.Second
	return cfg
}

// NewServerSession wraps an accepted net.Conn as a yamux server session.
// The returned session can accept multiplexed streams from the remote client.
func NewServerSession(conn net.Conn) (*yamux.Session, error) {
	return yamux.Server(conn, newConfig())
}

// NewClientSession wraps a dialed net.Conn as a yamux client session.
// The returned session can open multiplexed streams to the remote server.
func NewClientSession(conn net.Conn) (*yamux.Session, error) {
	return yamux.Client(conn, newConfig())
}
