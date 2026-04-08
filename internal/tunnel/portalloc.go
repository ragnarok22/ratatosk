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
	mu    sync.Mutex
	start int
	end   int
	used  map[int]bool
}

// NewPortAllocator creates a PortAllocator for the range [start, end).
func NewPortAllocator(start, end int) *PortAllocator {
	return &PortAllocator{
		start: start,
		end:   end,
		used:  make(map[int]bool),
	}
}

// Allocate picks a random available port in the range, verifies it is
// bindable on the OS, and marks it as used. Returns an error if no port
// could be allocated after several attempts.
func (p *PortAllocator) Allocate() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	rangeSize := p.end - p.start
	if rangeSize <= 0 {
		return 0, fmt.Errorf("invalid port range [%d, %d)", p.start, p.end)
	}

	for range 20 {
		port := p.start + rand.IntN(rangeSize)
		if p.used[port] {
			continue
		}
		// Verify the port is actually available on the OS.
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			continue
		}
		ln.Close()

		p.used[port] = true
		return port, nil
	}

	return 0, fmt.Errorf("failed to allocate port in range [%d, %d) after 20 attempts", p.start, p.end)
}

// Release marks a previously allocated port as available.
func (p *PortAllocator) Release(port int) {
	p.mu.Lock()
	delete(p.used, port)
	p.mu.Unlock()
}
