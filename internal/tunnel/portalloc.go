package tunnel

import (
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
)

// PortAllocator manages allocation of public ports for TCP/UDP tunnels
// within a configurable range.
type PortAllocator struct {
	mu        sync.Mutex
	start     int
	end       int
	used      map[int]bool
	available func(int) bool
}

// NewPortAllocator creates a PortAllocator for the range [start, end).
func NewPortAllocator(start, end int) *PortAllocator {
	return &PortAllocator{
		start: start,
		end:   end,
		used:  make(map[int]bool),
		available: func(port int) bool {
			ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
			if err != nil {
				return false
			}
			defer ln.Close()

			packetConn, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
			if err != nil {
				return false
			}
			packetConn.Close()
			return true
		},
	}
}

// Allocate scans the range from a random starting point, verifies each port is
// bindable for both TCP and UDP, and marks the first available port as used.
func (p *PortAllocator) Allocate() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	rangeSize := p.end - p.start
	if rangeSize <= 0 {
		return 0, fmt.Errorf("invalid port range [%d, %d)", p.start, p.end)
	}

	first := rand.IntN(rangeSize)
	for offset := range rangeSize {
		port := p.start + (first+offset)%rangeSize
		if p.used[port] {
			continue
		}
		if p.available(port) {
			p.used[port] = true
			return port, nil
		}
	}

	return 0, fmt.Errorf("no available port in range [%d, %d)", p.start, p.end)
}

// Release marks a previously allocated port as available.
func (p *PortAllocator) Release(port int) {
	p.mu.Lock()
	delete(p.used, port)
	p.mu.Unlock()
}
