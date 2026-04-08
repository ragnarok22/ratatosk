package inspector

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestStartServer(t *testing.T) {
	logger := NewLogger()
	addr, err := StartServer(logger)
	if err != nil {
		t.Fatalf("StartServer failed: %v", err)
	}
	if addr == "" {
		t.Fatal("expected non-empty address")
	}

	// GET /api/logs should return an empty JSON array.
	resp, err := http.Get("http://" + addr + "/api/logs")
	if err != nil {
		t.Fatalf("GET /api/logs failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var logs []TrafficLog
	if err := json.Unmarshal(body, &logs); err != nil {
		t.Fatalf("failed to unmarshal: %v\nbody: %s", err, body)
	}
	if len(logs) != 0 {
		t.Fatalf("expected 0 logs, got %d", len(logs))
	}

	// GET / should return HTML.
	resp2, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	ct := resp2.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html content type, got %q", ct)
	}
}

func TestStartServerPortFallback(t *testing.T) {
	// Occupy the first port so StartServer must fall back.
	first := fallbackPorts[0]
	ln, err := net.Listen("tcp", "127.0.0.1:4040")
	if err != nil {
		t.Skipf("could not bind port %d for test: %v", first, err)
	}
	defer ln.Close()

	logger := NewLogger()
	addr, err := StartServer(logger)
	if err != nil {
		t.Fatalf("StartServer failed with port %d occupied: %v", first, err)
	}

	// Should have fallen back to a different port.
	if strings.Contains(addr, "4040") {
		t.Fatalf("expected fallback port, got %s", addr)
	}
}

func TestStartServerAllPortsBusy(t *testing.T) {
	var listeners []net.Listener
	for _, port := range fallbackPorts {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			// Port already in use, we can't fully control the test.
			for _, l := range listeners {
				l.Close()
			}
			t.Skipf("could not bind port %d: %v", port, err)
			return
		}
		listeners = append(listeners, ln)
	}
	t.Cleanup(func() {
		for _, ln := range listeners {
			ln.Close()
		}
	})

	logger := NewLogger()
	_, err := StartServer(logger)
	if err == nil {
		t.Fatal("expected error when all ports are busy")
	}
}

func TestAPILogsAfterTraffic(t *testing.T) {
	logger := NewLogger()
	logger.Add(TrafficLog{Method: "POST", Path: "/data", RespStatus: 201})
	logger.Add(TrafficLog{Method: "GET", Path: "/data", RespStatus: 200})

	addr, err := StartServer(logger)
	if err != nil {
		t.Fatalf("StartServer failed: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/api/logs")
	if err != nil {
		t.Fatalf("GET /api/logs failed: %v", err)
	}
	defer resp.Body.Close()

	var logs []TrafficLog
	json.NewDecoder(resp.Body).Decode(&logs)

	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}
	if logs[0].Method != "POST" || logs[1].Method != "GET" {
		t.Fatalf("unexpected log entries: %+v", logs)
	}
}
