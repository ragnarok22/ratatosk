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

// ServeUDP reads datagrams from conn and relays them through yamux streams
// to the remote client. Each unique remote address gets its own yamux stream.
// Idle streams are cleaned up after udpSessionIdleTimeout.
func ServeUDP(ctx context.Context, conn *net.UDPConn, session *yamux.Session) {
	var mu sync.Mutex
	peers := make(map[string]*udpPeer)

	// Reaper goroutine cleans up idle peers.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				for addr, p := range peers {
					if time.Since(p.lastSeen) > udpSessionIdleTimeout {
						p.stream.Close()
						delete(peers, addr)
					}
				}
				mu.Unlock()
			}
		}
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
			slog.Error("udp read error", "error", err)
			continue
		}

		addrKey := remoteAddr.String()

		mu.Lock()
		peer, ok := peers[addrKey]
		if !ok {
			stream, err := session.Open()
			if err != nil {
				mu.Unlock()
				slog.Error("failed to open yamux stream for UDP peer", "addr", addrKey, "error", err)
				continue
			}
			peer = &udpPeer{stream: stream, lastSeen: time.Now()}
			peers[addrKey] = peer

			// Start a goroutine to read framed responses from the client
			// and send them back to this remote address.
			go udpStreamToConn(ctx, peer.stream, conn, remoteAddr, addrKey, &mu, peers)
		}
		peer.lastSeen = time.Now()
		mu.Unlock()

		if err := udpWriteFrame(peer.stream, buf[:n]); err != nil {
			slog.Error("failed to write UDP frame to stream", "addr", addrKey, "error", err)
			mu.Lock()
			peer.stream.Close()
			delete(peers, addrKey)
			mu.Unlock()
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
	mu *sync.Mutex,
	peers map[string]*udpPeer,
) {
	defer func() {
		mu.Lock()
		if p, ok := peers[addrKey]; ok && p.stream == stream {
			delete(peers, addrKey)
		}
		mu.Unlock()
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
