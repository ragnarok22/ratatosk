package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"ratatosk/internal/inspector"
	"ratatosk/internal/protocol"
	"ratatosk/internal/redact"
	"ratatosk/internal/tunnel"
)

func startMockRelay(t *testing.T, handler func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		handler(conn)
	}()
	return ln.Addr().String()
}

func TestRunVersionCommand(t *testing.T) {
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(
		[]string{"ratatosk", "version"},
		func(string) string { return "" },
		&stdout,
		&stderr,
		func(string) error {
			t.Fatal("updateCLI should not be called")
			return nil
		},
		func(string, int, string) error {
			t.Fatal("runClient should not be called")
			return nil
		},
	)

	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if got := stdout.String(); !strings.Contains(got, "Ratatosk CLI version:") {
		t.Fatalf("stdout = %q, want version output", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunSelfUpdateCommand(t *testing.T) {
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	t.Run("success", func(t *testing.T) {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		called := false

		code := run(
			[]string{"ratatosk", "self-update"},
			func(string) string { return "" },
			&stdout,
			&stderr,
			func(version string) error {
				called = true
				if version != Version {
					t.Fatalf("version = %q, want %q", version, Version)
				}
				return nil
			},
			func(string, int, string) error {
				t.Fatal("runClient should not be called")
				return nil
			},
		)

		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		if !called {
			t.Fatal("updateCLI was not called")
		}
	})

	t.Run("failure", func(t *testing.T) {
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		code := run(
			[]string{"ratatosk", "self-update"},
			func(string) string { return "" },
			&stdout,
			&stderr,
			func(string) error { return errors.New("boom") },
			func(string, int, string) error {
				t.Fatal("runClient should not be called")
				return nil
			},
		)

		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
	})
}

func TestRunUsesEnvOverride(t *testing.T) {
	oldLogger := slog.Default()
	oldRedact := redact.Enabled
	t.Cleanup(func() {
		slog.SetDefault(oldLogger)
		redact.Enabled = oldRedact
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	var gotServer string
	var gotPort int
	var gotAuth string

	code := run(
		[]string{"ratatosk", "-port", "4567", "-basic-auth", "admin:secret"},
		func(key string) string {
			if key == "RATATOSK_SERVER" {
				return "relay.example:7000"
			}
			return ""
		},
		&stdout,
		&stderr,
		func(string) error {
			t.Fatal("updateCLI should not be called")
			return nil
		},
		func(server string, port int, basicAuth string) error {
			gotServer = server
			gotPort = port
			gotAuth = basicAuth
			return nil
		},
	)

	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if gotServer != "relay.example:7000" {
		t.Fatalf("server = %q, want %q", gotServer, "relay.example:7000")
	}
	if gotPort != 4567 {
		t.Fatalf("port = %d, want 4567", gotPort)
	}
	if gotAuth != "admin:secret" {
		t.Fatalf("basicAuth = %q, want %q", gotAuth, "admin:secret")
	}
}

func TestRunExplicitServerBeatsEnvOverride(t *testing.T) {
	oldLogger := slog.Default()
	oldRedact := redact.Enabled
	t.Cleanup(func() {
		slog.SetDefault(oldLogger)
		redact.Enabled = oldRedact
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var gotServer string

	code := run(
		[]string{"ratatosk", "-server", "manual.example:1234"},
		func(key string) string {
			if key == "RATATOSK_SERVER" {
				return "relay.example:7000"
			}
			return ""
		},
		&stdout,
		&stderr,
		func(string) error {
			t.Fatal("updateCLI should not be called")
			return nil
		},
		func(server string, port int, basicAuth string) error {
			gotServer = server
			return nil
		},
	)

	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if gotServer != "manual.example:1234" {
		t.Fatalf("server = %q, want %q", gotServer, "manual.example:1234")
	}
}

func TestRunRejectsInvalidBasicAuth(t *testing.T) {
	oldLogger := slog.Default()
	oldRedact := redact.Enabled
	t.Cleanup(func() {
		slog.SetDefault(oldLogger)
		redact.Enabled = oldRedact
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(
		[]string{"ratatosk", "-basic-auth", "not-valid"},
		func(string) string { return "" },
		&stdout,
		&stderr,
		func(string) error {
			t.Fatal("updateCLI should not be called")
			return nil
		},
		func(string, int, string) error {
			t.Fatal("runClient should not be called")
			return nil
		},
	)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if got := stderr.String(); !strings.Contains(got, "--basic-auth must be in 'user:pass' format") {
		t.Fatalf("stderr = %q, want basic auth validation message", got)
	}
}

func TestRunStreamerFlagAndClientError(t *testing.T) {
	oldLogger := slog.Default()
	oldRedact := redact.Enabled
	t.Cleanup(func() {
		slog.SetDefault(oldLogger)
		redact.Enabled = oldRedact
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(
		[]string{"ratatosk", "-streamer"},
		func(string) string { return "" },
		&stdout,
		&stderr,
		func(string) error {
			t.Fatal("updateCLI should not be called")
			return nil
		},
		func(string, int, string) error { return errors.New("dial failed") },
	)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !redact.Enabled {
		t.Fatal("redact.Enabled = false, want true")
	}
}

func TestRunClientHappyPath(t *testing.T) {
	addr := startMockRelay(t, func(conn net.Conn) {
		defer conn.Close()
		session, err := tunnel.NewServerSession(conn)
		if err != nil {
			return
		}
		defer session.Close()

		cs, err := session.Accept()
		if err != nil {
			return
		}
		if _, err := protocol.ReadRequest(cs); err != nil {
			cs.Close()
			return
		}
		protocol.WriteResponse(cs, &protocol.TunnelResponse{
			Success:   true,
			Subdomain: "test-happy",
		})
		cs.Close()

		// Allow the client time to read the response and enter its Accept
		// loop before the deferred session/conn Close tears everything down.
		// Without this pause the race detector + CI resource pressure can
		// cause the client's WriteRequest to hit a "session shutdown" error.
		time.Sleep(100 * time.Millisecond)
	})

	if err := runClient(addr, 13001, ""); err != nil {
		t.Fatalf("runClient: %v", err)
	}
}

func TestRunClientConnectionRefused(t *testing.T) {
	// Bind a port and immediately close to guarantee nothing is listening.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	if err := runClient(addr, 3000, ""); err == nil {
		t.Fatal("expected connection error")
	}
}

func TestRunClientHandshakeFailure(t *testing.T) {
	addr := startMockRelay(t, func(conn net.Conn) {
		defer conn.Close()
		session, err := tunnel.NewServerSession(conn)
		if err != nil {
			return
		}
		defer session.Close()

		cs, err := session.Accept()
		if err != nil {
			return
		}
		protocol.ReadRequest(cs)
		protocol.WriteResponse(cs, &protocol.TunnelResponse{
			Success: false,
			Error:   "test rejection",
		})
		cs.Close()
	})

	err := runClient(addr, 3000, "")
	if err == nil {
		t.Fatal("expected error for rejected handshake")
	}
}

func TestRunClientBadServer(t *testing.T) {
	addr := startMockRelay(t, func(conn net.Conn) {
		conn.Write([]byte("garbage"))
		conn.Close()
	})

	// Allow a moment for the listener to be ready.
	time.Sleep(10 * time.Millisecond)

	err := runClient(addr, 3000, "")
	if err == nil {
		t.Fatal("expected error for bad server data")
	}
}

func TestRunClientAbruptDisconnect(t *testing.T) {
	addr := startMockRelay(t, func(conn net.Conn) {
		session, _ := tunnel.NewServerSession(conn)

		cs, _ := session.Accept()
		protocol.ReadRequest(cs)
		protocol.WriteResponse(cs, &protocol.TunnelResponse{Success: true, Subdomain: "test-abrupt"})
		cs.Close()

		// Let the client enter the Accept loop, then kill the connection
		// without a graceful yamux shutdown to trigger the non-EOF path.
		time.Sleep(100 * time.Millisecond)
		conn.Close()
	})

	if err := runClient(addr, 13002, ""); err != nil {
		t.Fatalf("runClient: %v", err)
	}
}

func TestRunClientServerClosesEarly(t *testing.T) {
	addr := startMockRelay(t, func(conn net.Conn) {
		session, _ := tunnel.NewServerSession(conn)
		// Close immediately without accepting any streams.
		session.Close()
		conn.Close()
	})

	err := runClient(addr, 3000, "")
	if err == nil {
		t.Fatal("expected error when server closes session before handshake")
	}
}

func TestRunClientReadResponseError(t *testing.T) {
	addr := startMockRelay(t, func(conn net.Conn) {
		session, _ := tunnel.NewServerSession(conn)
		cs, _ := session.Accept()
		protocol.ReadRequest(cs)
		// Close without writing a response.
		cs.Close()
		session.Close()
		conn.Close()
	})

	err := runClient(addr, 3000, "")
	if err == nil {
		t.Fatal("expected error when server closes without responding")
	}
}

func TestRunClientWithTraffic(t *testing.T) {
	// Local HTTP server that the client proxies to.
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer local.Close()
	_, portStr, _ := net.SplitHostPort(local.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	addr := startMockRelay(t, func(conn net.Conn) {
		defer conn.Close()
		session, _ := tunnel.NewServerSession(conn)
		defer session.Close()

		cs, _ := session.Accept()
		protocol.ReadRequest(cs)
		protocol.WriteResponse(cs, &protocol.TunnelResponse{Success: true, Subdomain: "test-traffic"})
		cs.Close()

		// Simulate an incoming HTTP request through the tunnel.
		time.Sleep(100 * time.Millisecond)
		stream, err := session.Open()
		if err != nil {
			return
		}
		req, _ := http.NewRequest("GET", "http://localhost/test", nil)
		req.Write(stream)
		http.ReadResponse(bufio.NewReader(stream), req)
		stream.Close()

		time.Sleep(100 * time.Millisecond)
	})

	if err := runClient(addr, port, ""); err != nil {
		t.Fatalf("runClient: %v", err)
	}
}

func TestRunClientWithBasicAuth(t *testing.T) {
	addr := startMockRelay(t, func(conn net.Conn) {
		defer conn.Close()
		session, err := tunnel.NewServerSession(conn)
		if err != nil {
			return
		}
		defer session.Close()

		cs, err := session.Accept()
		if err != nil {
			return
		}
		req, err := protocol.ReadRequest(cs)
		if err != nil {
			cs.Close()
			return
		}
		if req.BasicAuth != "admin:secret" {
			cs.Close()
			return
		}
		protocol.WriteResponse(cs, &protocol.TunnelResponse{
			Success:   true,
			Subdomain: "test-auth",
		})
		cs.Close()

		time.Sleep(100 * time.Millisecond)
	})

	if err := runClient(addr, 13003, "admin:secret"); err != nil {
		t.Fatalf("runClient: %v", err)
	}
}

func TestHandleStream(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer local.Close()

	clientConn, serverConn := net.Pipe()
	logger := inspector.NewLogger()

	go handleStream(serverConn, local.Listener.Addr().String(), logger)

	fmt.Fprintf(clientConn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")

	resp, err := http.ReadResponse(bufio.NewReader(clientConn), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	resp.Body.Close()
	clientConn.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRunClientStreamerBanner(t *testing.T) {
	redact.Enabled = true
	t.Cleanup(func() { redact.Enabled = false })

	// Capture stdout to verify the banner is redacted.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	addr := startMockRelay(t, func(conn net.Conn) {
		defer conn.Close()
		session, err := tunnel.NewServerSession(conn)
		if err != nil {
			return
		}
		defer session.Close()

		cs, err := session.Accept()
		if err != nil {
			return
		}
		protocol.ReadRequest(cs)
		protocol.WriteResponse(cs, &protocol.TunnelResponse{
			Success:   true,
			Subdomain: "test-streamer",
			URL:       "http://test-streamer.localhost:8080",
		})
		cs.Close()

		// Same stabilisation pause as TestRunClientHappyPath — see comment there.
		time.Sleep(100 * time.Millisecond)
	})

	runClient(addr, 9999, "")

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stdout = old

	output := buf.String()

	// The local port should be redacted in the banner.
	if strings.Contains(output, "9999") {
		t.Errorf("local port leaked in streamer mode: %s", output)
	}
	if !strings.Contains(output, "http://test-streamer.localhost:8080") {
		t.Errorf("subdomain URL missing from banner: %s", output)
	}
	if !strings.Contains(output, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in banner: %s", output)
	}
}
