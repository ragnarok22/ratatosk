package tunnel

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

const udpSessionIdleTimeout = 60 * time.Second

var (
	udpReadFrame  = ReadFrame
	udpWriteFrame = WriteFrame
)

type udpPeer struct {
	stream   net.Conn
	lastSeen time.Time
}

// peerManager tracks UDP peers and their associated yamux streams,
// providing thread-safe access and idle peer cleanup.
type peerManager struct {
	mu    sync.Mutex
	peers map[string]*udpPeer
}

func newPeerManager() *peerManager {
	return &peerManager{peers: make(map[string]*udpPeer)}
}

// getOrCreate returns the peer for addrKey, creating a new one with the given
// stream if it doesn't exist. The second return value is true when a new peer
// was created (the caller should start the response reader goroutine).
func (pm *peerManager) getOrCreate(addrKey string, openStream func() (net.Conn, error)) (*udpPeer, bool, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if peer, ok := pm.peers[addrKey]; ok {
		peer.lastSeen = time.Now()
		return peer, false, nil
	}

	stream, err := openStream()
	if err != nil {
		return nil, false, err
	}
	peer := &udpPeer{stream: stream, lastSeen: time.Now()}
	pm.peers[addrKey] = peer
	return peer, true, nil
}

// remove deletes the peer for addrKey and closes its stream.
func (pm *peerManager) remove(addrKey string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if p, ok := pm.peers[addrKey]; ok {
		p.stream.Close()
		delete(pm.peers, addrKey)
	}
}

// removeIfStream deletes the peer for addrKey only if its stream matches
// the provided one (to avoid removing a replacement peer).
func (pm *peerManager) removeIfStream(addrKey string, stream net.Conn) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if p, ok := pm.peers[addrKey]; ok && p.stream == stream {
		delete(pm.peers, addrKey)
	}
}

// reapIdle removes peers that have been idle longer than the timeout.
func (pm *peerManager) reapIdle() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for addr, p := range pm.peers {
		if time.Since(p.lastSeen) > udpSessionIdleTimeout {
			p.stream.Close()
			delete(pm.peers, addr)
		}
	}
}

// startReaper runs a background goroutine that periodically removes idle peers.
// It returns when ctx is cancelled.
func (pm *peerManager) startReaper(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pm.reapIdle()
		}
	}
}

// ServeUDP reads datagrams from conn and relays them through yamux streams
// to the remote client. Each unique remote address gets its own yamux stream.
// Idle streams are cleaned up after udpSessionIdleTimeout.
func ServeUDP(ctx context.Context, conn *net.UDPConn, session *yamux.Session) {
	pm := newPeerManager()
	go pm.startReaper(ctx)

	buf := make([]byte, MaxUDPFrameSize)
	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			slog.Error("udp read error", "error", err)
			continue
		}

		addrKey := remoteAddr.String()

		peer, isNew, err := pm.getOrCreate(addrKey, session.Open)
		if err != nil {
			slog.Error("failed to open yamux stream for UDP peer", "addr", addrKey, "error", err)
			continue
		}

		if isNew {
			go udpStreamToConn(ctx, peer.stream, conn, remoteAddr, addrKey, pm)
		}

		if err := udpWriteFrame(peer.stream, buf[:n]); err != nil {
			slog.Error("failed to write UDP frame to stream", "addr", addrKey, "error", err)
			pm.remove(addrKey)
		}
	}
}

// udpStreamToConn reads framed responses from a yamux stream and writes
// them as UDP datagrams back to the remote address.
func udpStreamToConn(
	ctx context.Context,
	stream net.Conn,
	conn *net.UDPConn,
	remoteAddr *net.UDPAddr,
	addrKey string,
	pm *peerManager,
) {
	defer func() {
		pm.removeIfStream(addrKey, stream)
		stream.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		data, err := udpReadFrame(stream)
		if err != nil {
			return
		}
		if _, err := conn.WriteToUDP(data, remoteAddr); err != nil {
			slog.Error("failed to write UDP response", "addr", addrKey, "error", err)
			return
		}
	}
}
