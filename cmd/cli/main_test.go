package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
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

var noopRawClient = func(string, int, string) error { return nil }

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
		noopRawClient,
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

func TestMainVersionCommand(t *testing.T) {
	oldArgs := os.Args
	oldStdout := os.Stdout
	t.Cleanup(func() {
		os.Args = oldArgs
		os.Stdout = oldStdout
	})

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()

	os.Args = []string{"ratatosk", "version"}
	os.Stdout = w

	main()

	w.Close()
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !strings.Contains(string(output), "Ratatosk CLI version:") {
		t.Fatalf("stdout = %q, want version output", string(output))
	}
}

func TestMainExitsOnRunError(t *testing.T) {
	oldArgs := cliArgs
	oldGetenv := cliGetenv
	oldStdout := cliStdout
	oldStderr := cliStderr
	oldExit := cliExit
	oldUpdateCLI := cliUpdateCLI
	oldRunClient := cliRunClient
	oldRunRawClient := cliRunRawClient
	t.Cleanup(func() {
		cliArgs = oldArgs
		cliGetenv = oldGetenv
		cliStdout = oldStdout
		cliStderr = oldStderr
		cliExit = oldExit
		cliUpdateCLI = oldUpdateCLI
		cliRunClient = oldRunClient
		cliRunRawClient = oldRunRawClient
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := -1

	cliArgs = func() []string { return []string{"ratatosk", "-basic-auth", "invalid"} }
	cliGetenv = func(string) string { return "" }
	cliStdout = &stdout
	cliStderr = &stderr
	cliUpdateCLI = func(string) error { return nil }
	cliRunClient = func(string, int, string) error { return nil }
	cliRunRawClient = func(string, int, string) error { return nil }
	cliExit = func(code int) { exitCode = code }

	main()

	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "--basic-auth must be in 'user:pass' format") {
		t.Fatalf("stderr = %q, want validation error", stderr.String())
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
			noopRawClient,
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
			noopRawClient,
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
		noopRawClient,
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
		noopRawClient,
	)

	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if gotServer != "manual.example:1234" {
		t.Fatalf("server = %q, want %q", gotServer, "manual.example:1234")
	}
}

func TestRunTCPCommandAcceptsServerFlagAfterPort(t *testing.T) {
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var gotServer string
	var gotPort int
	var gotProto string

	code := run(
		[]string{"ratatosk", "tcp", "8080", "--server", "manual.example:1234"},
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
		func(server string, port int, proto string) error {
			gotServer = server
			gotPort = port
			gotProto = proto
			return nil
		},
	)

	if code != 0 {
		t.Fatalf("code = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	if gotServer != "manual.example:1234" {
		t.Fatalf("server = %q, want %q", gotServer, "manual.example:1234")
	}
	if gotPort != 8080 {
		t.Fatalf("port = %d, want 8080", gotPort)
	}
	if gotProto != protocol.ProtoTCP {
		t.Fatalf("proto = %q, want %q", gotProto, protocol.ProtoTCP)
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
		noopRawClient,
	)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if got := stderr.String(); !strings.Contains(got, "--basic-auth must be in 'user:pass' format") {
		t.Fatalf("stderr = %q, want basic auth validation message", got)
	}
}

func TestRunFlagParseError(t *testing.T) {
	oldLogger := slog.Default()
	oldRedact := redact.Enabled
	t.Cleanup(func() {
		slog.SetDefault(oldLogger)
		redact.Enabled = oldRedact
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(
		[]string{"ratatosk", "-unknown"},
		func(string) string { return "" },
		&stdout,
		&stderr,
		func(string) error { return nil },
		func(string, int, string) error { return nil },
		noopRawClient,
	)

	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
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
		noopRawClient,
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

func TestRunClientUsesFallbackURLWhenResponseURLMissing(t *testing.T) {
	oldStartInspector := cliStartInspector
	oldStdout := os.Stdout
	t.Cleanup(func() {
		cliStartInspector = oldStartInspector
		os.Stdout = oldStdout
	})

	cliStartInspector = func(*inspector.Logger) (string, error) {
		return "", errors.New("inspector unavailable")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
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
		if _, err := protocol.ReadRequest(cs); err != nil {
			cs.Close()
			return
		}
		protocol.WriteResponse(cs, &protocol.TunnelResponse{
			Success:   true,
			Subdomain: "fallback-url",
		})
		cs.Close()

		time.Sleep(100 * time.Millisecond)
	})

	if err := runClient(addr, 13004, ""); err != nil {
		t.Fatalf("runClient: %v", err)
	}

	w.Close()
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	got := string(output)
	if !strings.Contains(got, "http://fallback-url.localhost:8080") {
		t.Fatalf("stdout = %q, want fallback tunnel URL", got)
	}
	if strings.Contains(got, "Web Interface") {
		t.Fatalf("stdout = %q, want inspector banner omitted when start fails", got)
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

func TestRunTCPCommand(t *testing.T) {
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	var gotServer string
	var gotPort int
	var gotProto string

	code := run(
		[]string{"ratatosk", "tcp", "22"},
		func(string) string { return "" },
		&stdout,
		&stderr,
		func(string) error { return nil },
		func(string, int, string) error { return nil },
		func(server string, port int, proto string) error {
			gotServer = server
			gotPort = port
			gotProto = proto
			return nil
		},
	)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if gotServer != "localhost:7000" {
		t.Errorf("server = %q, want %q", gotServer, "localhost:7000")
	}
	if gotPort != 22 {
		t.Errorf("port = %d, want 22", gotPort)
	}
	if gotProto != protocol.ProtoTCP {
		t.Errorf("proto = %q, want %q", gotProto, protocol.ProtoTCP)
	}
}

func TestRunUDPCommand(t *testing.T) {
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	var gotPort int
	var gotProto string

	code := run(
		[]string{"ratatosk", "udp", "25565"},
		func(string) string { return "" },
		&stdout,
		&stderr,
		func(string) error { return nil },
		func(string, int, string) error { return nil },
		func(server string, port int, proto string) error {
			gotPort = port
			gotProto = proto
			return nil
		},
	)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if gotPort != 25565 {
		t.Errorf("port = %d, want 25565", gotPort)
	}
	if gotProto != protocol.ProtoUDP {
		t.Errorf("proto = %q, want %q", gotProto, protocol.ProtoUDP)
	}
}

func TestRunTCPCommandMissingPort(t *testing.T) {
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(
		[]string{"ratatosk", "tcp"},
		func(string) string { return "" },
		&stdout,
		&stderr,
		func(string) error { return nil },
		func(string, int, string) error { return nil },
		noopRawClient,
	)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("stderr = %q, want usage message", stderr.String())
	}
}

func TestRunTCPCommandInvalidPort(t *testing.T) {
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(
		[]string{"ratatosk", "tcp", "notanumber"},
		func(string) string { return "" },
		&stdout,
		&stderr,
		func(string) error { return nil },
		func(string, int, string) error { return nil },
		noopRawClient,
	)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "invalid port") {
		t.Errorf("stderr = %q, want invalid port message", stderr.String())
	}
}

func TestRunTCPCommandFlagParseError(t *testing.T) {
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(
		[]string{"ratatosk", "tcp", "--unknown"},
		func(string) string { return "" },
		&stdout,
		&stderr,
		func(string) error { return nil },
		func(string, int, string) error { return nil },
		noopRawClient,
	)

	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestRunTCPCommandEnvOverride(t *testing.T) {
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var gotServer string

	code := run(
		[]string{"ratatosk", "tcp", "22"},
		func(key string) string {
			if key == "RATATOSK_SERVER" {
				return "relay.example:7000"
			}
			return ""
		},
		&stdout,
		&stderr,
		func(string) error { return nil },
		func(string, int, string) error { return nil },
		func(server string, port int, proto string) error {
			gotServer = server
			return nil
		},
	)

	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if gotServer != "relay.example:7000" {
		t.Errorf("server = %q, want %q", gotServer, "relay.example:7000")
	}
}

func TestRunTCPCommandClientError(t *testing.T) {
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(
		[]string{"ratatosk", "tcp", "22"},
		func(string) string { return "" },
		&stdout,
		&stderr,
		func(string) error { return nil },
		func(string, int, string) error { return nil },
		func(string, int, string) error { return errors.New("connection refused") },
	)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}

func TestRunRawClientHappyPath(t *testing.T) {
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
		if req.Protocol != protocol.ProtoTCP {
			cs.Close()
			return
		}
		protocol.WriteResponse(cs, &protocol.TunnelResponse{
			Success: true,
			Port:    12345,
		})
		cs.Close()

		time.Sleep(100 * time.Millisecond)
	})

	if err := runRawClient(addr, 22, protocol.ProtoTCP); err != nil {
		t.Fatalf("runRawClient: %v", err)
	}
}

func TestRunRawClientHandshakeFailure(t *testing.T) {
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
			Error:   "port exhausted",
		})
		cs.Close()
	})

	err := runRawClient(addr, 22, protocol.ProtoTCP)
	if err == nil {
		t.Fatal("expected error for rejected handshake")
	}
	if !strings.Contains(err.Error(), "port exhausted") {
		t.Errorf("error = %q, want to contain 'port exhausted'", err)
	}
}

func TestRunRawClientConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	if err := runRawClient(addr, 22, protocol.ProtoTCP); err == nil {
		t.Fatal("expected connection error")
	}
}

func TestRunRawClientReadResponseError(t *testing.T) {
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
		cs.Close()
	})

	if err := runRawClient(addr, 22, protocol.ProtoTCP); err == nil {
		t.Fatal("expected response read error")
	}
}

func TestRunRawClientAbruptDisconnect(t *testing.T) {
	addr := startMockRelay(t, func(conn net.Conn) {
		session, err := tunnel.NewServerSession(conn)
		if err != nil {
			conn.Close()
			return
		}

		cs, err := session.Accept()
		if err != nil {
			conn.Close()
			return
		}
		protocol.ReadRequest(cs)
		protocol.WriteResponse(cs, &protocol.TunnelResponse{Success: true, Port: 12345})
		cs.Close()

		time.Sleep(100 * time.Millisecond)
		conn.Close()
	})

	if err := runRawClient(addr, 22, protocol.ProtoTCP); err != nil {
		t.Fatalf("runRawClient: %v", err)
	}
}

func TestRunRawClientUDPPath(t *testing.T) {
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer udpConn.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)

		buf := make([]byte, tunnel.MaxUDPFrameSize)
		n, addr, err := udpConn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if string(buf[:n]) != "ping" {
			t.Errorf("got %q, want %q", string(buf[:n]), "ping")
			return
		}
		if _, err := udpConn.WriteToUDP([]byte("pong"), addr); err != nil {
			t.Errorf("WriteToUDP: %v", err)
		}
	}()

	_, portStr, err := net.SplitHostPort(udpConn.LocalAddr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("Atoi: %v", err)
	}

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
		if req.Protocol != protocol.ProtoUDP {
			t.Errorf("protocol = %q, want %q", req.Protocol, protocol.ProtoUDP)
			cs.Close()
			return
		}
		protocol.WriteResponse(cs, &protocol.TunnelResponse{Success: true, Port: 23456})
		cs.Close()

		time.Sleep(100 * time.Millisecond)

		stream, err := session.Open()
		if err != nil {
			return
		}
		defer stream.Close()

		if err := tunnel.WriteFrame(stream, []byte("ping")); err != nil {
			t.Errorf("WriteFrame: %v", err)
			return
		}
		frame, err := tunnel.ReadFrame(stream)
		if err != nil {
			t.Errorf("ReadFrame: %v", err)
			return
		}
		if string(frame) != "pong" {
			t.Errorf("got %q, want %q", string(frame), "pong")
		}

		time.Sleep(100 * time.Millisecond)
	})

	if err := runRawClient(addr, port, protocol.ProtoUDP); err != nil {
		t.Fatalf("runRawClient: %v", err)
	}

	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("local UDP server did not finish")
	}
}

func TestHandleTCPStream(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	localDone := make(chan struct{})
	go func() {
		defer close(localDone)

		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			t.Errorf("ReadFull: %v", err)
			return
		}
		if string(buf) != "ping" {
			t.Errorf("got %q, want %q", string(buf), "ping")
			return
		}

		if _, err := conn.Write([]byte("pong")); err != nil {
			t.Errorf("Write: %v", err)
		}
	}()

	clientStream, serverStream := net.Pipe()
	done := make(chan struct{})
	go func() {
		handleTCPStream(serverStream, ln.Addr().String())
		close(done)
	}()

	if _, err := clientStream.Write([]byte("ping")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, 4)
	if _, err := io.ReadFull(clientStream, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(buf) != "pong" {
		t.Fatalf("got %q, want %q", string(buf), "pong")
	}

	clientStream.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleTCPStream did not return")
	}

	select {
	case <-localDone:
	case <-time.After(2 * time.Second):
		t.Fatal("local TCP server did not finish")
	}
}

func TestHandleTCPStreamDialError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	clientStream, serverStream := net.Pipe()
	done := make(chan struct{})
	go func() {
		handleTCPStream(serverStream, addr)
		close(done)
	}()

	clientStream.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleTCPStream did not return on dial failure")
	}
}

func TestHandleUDPStream(t *testing.T) {
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer udpConn.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)

		buf := make([]byte, tunnel.MaxUDPFrameSize)
		n, addr, err := udpConn.ReadFromUDP(buf)
		if err != nil {
			t.Errorf("ReadFromUDP: %v", err)
			return
		}
		if string(buf[:n]) != "ping" {
			t.Errorf("got %q, want %q", string(buf[:n]), "ping")
			return
		}

		if _, err := udpConn.WriteToUDP([]byte("pong"), addr); err != nil {
			t.Errorf("WriteToUDP pong: %v", err)
			return
		}

		time.Sleep(50 * time.Millisecond)
		if _, err := udpConn.WriteToUDP([]byte("late"), addr); err != nil {
			t.Errorf("WriteToUDP late: %v", err)
		}
	}()

	clientStream, serverStream := net.Pipe()
	done := make(chan struct{})
	go func() {
		handleUDPStream(serverStream, udpConn.LocalAddr().String())
		close(done)
	}()

	if err := tunnel.WriteFrame(clientStream, []byte("ping")); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	frame, err := tunnel.ReadFrame(clientStream)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if string(frame) != "pong" {
		t.Fatalf("got %q, want %q", string(frame), "pong")
	}

	clientStream.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleUDPStream did not return")
	}

	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("local UDP server did not finish")
	}
}

func TestHandleUDPStreamResolveError(t *testing.T) {
	clientStream, serverStream := net.Pipe()
	done := make(chan struct{})
	go func() {
		handleUDPStream(serverStream, "bad udp addr")
		close(done)
	}()

	clientStream.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleUDPStream did not return on resolve failure")
	}
}

func TestHandleUDPStreamDialError(t *testing.T) {
	oldResolveUDPAddr := cliResolveUDPAddr
	oldDialUDP := cliDialUDP
	t.Cleanup(func() {
		cliResolveUDPAddr = oldResolveUDPAddr
		cliDialUDP = oldDialUDP
	})

	cliResolveUDPAddr = func(network, address string) (*net.UDPAddr, error) {
		return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9999}, nil
	}
	cliDialUDP = func(network string, laddr, raddr *net.UDPAddr) (*net.UDPConn, error) {
		return nil, errors.New("dial failed")
	}

	clientStream, serverStream := net.Pipe()
	done := make(chan struct{})
	go func() {
		handleUDPStream(serverStream, "127.0.0.1:9999")
		close(done)
	}()

	clientStream.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleUDPStream did not return on dial failure")
	}
}
