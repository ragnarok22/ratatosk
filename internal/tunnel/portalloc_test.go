package tunnel

import (
	"sync"
	"testing"
)

func TestAllocateAndRelease(t *testing.T) {
	pa := NewPortAllocator(30000, 30010)

	port, err := pa.Allocate()
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if port < 30000 || port >= 30010 {
		t.Fatalf("port %d out of range [30000, 30010)", port)
	}

	pa.Release(port)

	// Should be able to allocate again after release.
	port2, err := pa.Allocate()
	if err != nil {
		t.Fatalf("Allocate after release: %v", err)
	}
	if port2 < 30000 || port2 >= 30010 {
		t.Fatalf("port %d out of range [30000, 30010)", port2)
	}
}

func TestAllocateExhausted(t *testing.T) {
	pa := NewPortAllocator(30100, 30103)

	var allocated []int
	for range 3 {
		port, err := pa.Allocate()
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		allocated = append(allocated, port)
	}

	_, err := pa.Allocate()
	if err == nil {
		t.Fatal("expected error when port range exhausted")
	}

	// Release one and try again.
	pa.Release(allocated[0])
	port, err := pa.Allocate()
	if err != nil {
		t.Fatalf("Allocate after release: %v", err)
	}
	if port != allocated[0] {
		// Port may differ since random, but must be in range.
		if port < 30100 || port >= 30103 {
			t.Fatalf("port %d out of range [30100, 30103)", port)
		}
	}
}

func TestAllocateInvalidRange(t *testing.T) {
	pa := NewPortAllocator(100, 100)
	_, err := pa.Allocate()
	if err == nil {
		t.Fatal("expected error for zero-width range")
	}
}

func TestConcurrentAllocate(t *testing.T) {
	pa := NewPortAllocator(31000, 31100)

	const n = 20
	ports := make(chan int, n)
	errs := make(chan error, n)

	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			port, err := pa.Allocate()
			if err != nil {
				errs <- err
				return
			}
			ports <- port
		}()
	}
	wg.Wait()
	close(ports)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent Allocate: %v", err)
	}

	seen := make(map[int]bool)
	for port := range ports {
		if seen[port] {
			t.Fatalf("duplicate port allocated: %d", port)
		}
		seen[port] = true
	}
}

func TestReleaseUnallocated(t *testing.T) {
	pa := NewPortAllocator(32000, 32010)
	// Should not panic.
	pa.Release(32005)
}
