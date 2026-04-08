package inspector

import (
	"sync"
	"testing"
	"time"
)

func TestNewLogger(t *testing.T) {
	l := NewLogger()
	if l == nil {
		t.Fatal("NewLogger returned nil")
	}
	if got := len(l.Entries()); got != 0 {
		t.Fatalf("expected 0 entries, got %d", got)
	}
}

func TestLoggerAdd(t *testing.T) {
	l := NewLogger()
	id := l.Add(TrafficLog{
		Method: "GET",
		Path:   "/hello",
	})
	if id != 0 {
		t.Fatalf("expected first ID 0, got %d", id)
	}

	entries := l.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Method != "GET" || entries[0].Path != "/hello" {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
}

func TestLoggerRingBuffer(t *testing.T) {
	l := NewLogger()
	for i := 0; i < 60; i++ {
		l.Add(TrafficLog{Method: "GET", Path: "/"})
	}

	entries := l.Entries()
	if len(entries) != maxEntries {
		t.Fatalf("expected %d entries, got %d", maxEntries, len(entries))
	}
	// First 10 should have been evicted; oldest remaining ID is 10.
	if entries[0].ID != 10 {
		t.Fatalf("expected first entry ID 10, got %d", entries[0].ID)
	}
	if entries[len(entries)-1].ID != 59 {
		t.Fatalf("expected last entry ID 59, got %d", entries[len(entries)-1].ID)
	}
}

func TestLoggerEntriesReturnsCopy(t *testing.T) {
	l := NewLogger()
	l.Add(TrafficLog{Method: "GET", Path: "/a"})

	entries := l.Entries()
	entries[0].Path = "/modified"

	original := l.Entries()
	if original[0].Path != "/a" {
		t.Fatal("Entries did not return a defensive copy")
	}
}

func TestLoggerConcurrent(t *testing.T) {
	l := NewLogger()
	var wg sync.WaitGroup
	n := 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			l.Add(TrafficLog{Method: "POST", Path: "/concurrent", Timestamp: time.Now()})
		}()
	}
	wg.Wait()

	entries := l.Entries()
	if len(entries) != n {
		t.Fatalf("expected %d entries, got %d", n, len(entries))
	}

	ids := make(map[int]bool)
	for _, e := range entries {
		if ids[e.ID] {
			t.Fatalf("duplicate ID: %d", e.ID)
		}
		ids[e.ID] = true
	}
}

func TestTruncateBody(t *testing.T) {
	small := make([]byte, 100)
	for i := range small {
		small[i] = 'a'
	}
	if got := TruncateBody(small); len(got) != 100 {
		t.Fatalf("expected 100 chars, got %d", len(got))
	}

	exact := make([]byte, maxBodyLog)
	if got := TruncateBody(exact); len(got) != maxBodyLog {
		t.Fatalf("expected %d chars, got %d", maxBodyLog, len(got))
	}

	large := make([]byte, maxBodyLog+500)
	if got := TruncateBody(large); len(got) != maxBodyLog {
		t.Fatalf("expected %d chars, got %d", maxBodyLog, len(got))
	}
}
