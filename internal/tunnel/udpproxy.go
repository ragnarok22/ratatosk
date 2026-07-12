package tunnel

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

const (
	udpSessionIdleTimeout = 60 * time.Second
	udpStreamWriteTimeout = 5 * time.Second
	maxUDPPeers           = 1024
)

var errTooManyUDPPeers = errors.New("maximum UDP peer count reached")

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
	if peer, ok := pm.peers[addrKey]; ok {
		peer.lastSeen = time.Now()
		pm.mu.Unlock()
		return peer, false, nil
	}
	if len(pm.peers) >= maxUDPPeers {
		pm.mu.Unlock()
		return nil, false, errTooManyUDPPeers
	}
	pm.mu.Unlock()

	stream, err := openStream()
	if err != nil {
		return nil, false, err
	}

	pm.mu.Lock()
	if peer, ok := pm.peers[addrKey]; ok {
		peer.lastSeen = time.Now()
		pm.mu.Unlock()
		stream.Close()
		return peer, false, nil
	}
	if len(pm.peers) >= maxUDPPeers {
		pm.mu.Unlock()
		stream.Close()
		return nil, false, errTooManyUDPPeers
	}
	peer := &udpPeer{stream: stream, lastSeen: time.Now()}
	pm.peers[addrKey] = peer
	pm.mu.Unlock()
	return peer, true, nil
}

// remove deletes the peer for addrKey and closes its stream.
func (pm *peerManager) remove(addrKey string) {
	pm.mu.Lock()
	p, ok := pm.peers[addrKey]
	if ok {
		delete(pm.peers, addrKey)
	}
	pm.mu.Unlock()
	if ok {
		p.stream.Close()
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
	var streams []net.Conn
	for addr, p := range pm.peers {
		if time.Since(p.lastSeen) > udpSessionIdleTimeout {
			streams = append(streams, p.stream)
			delete(pm.peers, addr)
		}
	}
	pm.mu.Unlock()
	for _, stream := range streams {
		stream.Close()
	}
}

func (pm *peerManager) closeAll() {
	pm.mu.Lock()
	streams := make([]net.Conn, 0, len(pm.peers))
	for addr, peer := range pm.peers {
		streams = append(streams, peer.stream)
		delete(pm.peers, addr)
	}
	pm.mu.Unlock()

	for _, stream := range streams {
		stream.Close()
	}
}

// startReaper runs a background goroutine that periodically removes idle peers.
// It returns when ctx is canceled.
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
	defer pm.closeAll()
	reaperCtx, stopReaper := context.WithCancel(ctx)
	defer stopReaper()
	go pm.startReaper(reaperCtx)

	stopReadInterrupt := make(chan struct{})
	readInterruptDone := make(chan struct{})
	go func() {
		defer close(readInterruptDone)
		select {
		case <-ctx.Done():
			conn.SetReadDeadline(time.Now())
		case <-stopReadInterrupt:
		}
	}()
	defer func() {
		close(stopReadInterrupt)
		<-readInterruptDone
		conn.SetReadDeadline(time.Time{})
	}()

	buf := make([]byte, MaxUDPFrameSize)
	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			slog.Error("udp read error", "error", err)
			return
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

		if err := peer.stream.SetWriteDeadline(time.Now().Add(udpStreamWriteTimeout)); err != nil {
			slog.Error("failed to set UDP stream write deadline", "addr", addrKey, "error", err)
			pm.remove(addrKey)
			continue
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
	stopCancel := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			stream.Close()
		case <-stopCancel:
		}
	}()
	defer func() {
		close(stopCancel)
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
