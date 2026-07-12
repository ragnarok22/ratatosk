package control

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testToken      = "0123456789abcdef0123456789abcdef"
	testOtherToken = "abcdef0123456789abcdef0123456789"
	testTimeout    = time.Second
)

func TestAuthenticationSuccess(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- AuthenticateAsServer(server, testToken, testTimeout)
	}()

	if err := AuthenticateAsClient(client, testToken, testTimeout); err != nil {
		t.Fatalf("AuthenticateAsClient: %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("AuthenticateAsServer: %v", err)
	}
}

func TestAuthenticationRejectsWrongAndMissingTokens(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected string
	}{
		{name: "wrong", token: testOtherToken, expected: testToken},
		{name: "missing", expected: testToken},
		{name: "server token missing", token: testToken},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()

			serverErr := make(chan error, 1)
			go func() {
				serverErr <- AuthenticateAsServer(server, tt.expected, testTimeout)
			}()

			clientErr := AuthenticateAsClient(client, tt.token, testTimeout)
			if !errors.Is(clientErr, ErrAuthenticationFailed) {
				t.Fatalf("client error = %v, want ErrAuthenticationFailed", clientErr)
			}
			gotServerErr := <-serverErr
			if !errors.Is(gotServerErr, ErrAuthenticationFailed) {
				t.Fatalf("server error = %v, want ErrAuthenticationFailed", gotServerErr)
			}
			for _, secret := range []string{tt.expected, tt.token} {
				if secret != "" && (strings.Contains(clientErr.Error(), secret) || strings.Contains(gotServerErr.Error(), secret)) {
					t.Fatalf("authentication error contains token %q", secret)
				}
			}
		})
	}
}

func TestServerRejectsVersionMismatchGenerically(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- AuthenticateAsServer(server, testToken, testTimeout)
	}()

	if _, err := io.WriteString(client, `{"version":2,"token":"`+testToken+`"}`+"\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}
	assertGenericFailureResponse(t, client)
	if err := <-serverErr; !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("server error = %v, want ErrAuthenticationFailed", err)
	}
}

func TestServerRejectsInvalidRecordsGenerically(t *testing.T) {
	tests := []struct {
		name    string
		request string
	}{
		{
			name:    "unknown field",
			request: `{"version":1,"token":"` + testToken + `","unknown":true}` + "\n",
		},
		{
			name:    "trailing JSON",
			request: `{"version":1,"token":"` + testToken + `"}{}` + "\n",
		},
		{
			name:    "malformed JSON",
			request: `{"version":1,"token":` + testToken + "\n",
		},
		{
			name:    "oversized",
			request: strings.Repeat("x", maxRecordSize+1) + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server := net.Pipe()
			serverErr := make(chan error, 1)
			go func() {
				serverErr <- AuthenticateAsServer(server, testToken, testTimeout)
			}()

			writeErr := make(chan error, 1)
			go func() {
				_, err := io.WriteString(client, tt.request)
				writeErr <- err
			}()

			assertGenericFailureResponse(t, client)
			client.Close()
			server.Close()
			if err := <-serverErr; !errors.Is(err, ErrAuthenticationFailed) {
				t.Fatalf("server error = %v, want ErrAuthenticationFailed", err)
			}
			<-writeErr
		})
	}
}

func TestClientRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{name: "unknown field", response: `{"success":true,"unknown":true}` + "\n"},
		{name: "trailing JSON", response: `{"success":true}{}` + "\n"},
		{name: "oversized", response: strings.Repeat("x", maxRecordSize+1) + "\n"},
		{name: "missing success", response: `{"error":"authentication failed"}` + "\n"},
		{name: "missing failure error", response: `{"success":false}` + "\n"},
		{name: "error on success", response: `{"success":true,"error":"authentication failed"}` + "\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server := net.Pipe()
			peerErr := make(chan error, 1)
			go func() {
				var request authRequest
				if err := readRecord(server, &request); err != nil {
					peerErr <- err
					return
				}
				_, err := io.WriteString(server, tt.response)
				peerErr <- err
			}()

			err := AuthenticateAsClient(client, testToken, testTimeout)
			if err == nil {
				t.Fatal("AuthenticateAsClient accepted an invalid response")
			}
			if strings.Contains(err.Error(), testToken) {
				t.Fatal("client error contains token")
			}
			client.Close()
			server.Close()
			<-peerErr
		})
	}
}

func TestAuthenticationTimeouts(t *testing.T) {
	tests := []struct {
		name string
		run  func(net.Conn) error
	}{
		{
			name: "client",
			run: func(conn net.Conn) error {
				return AuthenticateAsClient(conn, testToken, 20*time.Millisecond)
			},
		},
		{
			name: "server",
			run: func(conn net.Conn) error {
				return AuthenticateAsServer(conn, testToken, 20*time.Millisecond)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, peer := net.Pipe()
			defer conn.Close()
			defer peer.Close()

			started := time.Now()
			err := tt.run(conn)
			if err == nil {
				t.Fatal("authentication did not time out")
			}
			var netErr net.Error
			if !errors.As(err, &netErr) || !netErr.Timeout() {
				t.Fatalf("error = %v, want network timeout", err)
			}
			if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
				t.Fatalf("timeout took %v, want less than 500ms", elapsed)
			}
		})
	}
}

func TestAuthenticationClearsDeadlines(t *testing.T) {
	clientPipe, serverPipe := net.Pipe()
	client := &deadlineConn{Conn: clientPipe}
	server := &deadlineConn{Conn: serverPipe}
	defer client.Close()
	defer server.Close()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- AuthenticateAsServer(server, testToken, testTimeout)
	}()
	if err := AuthenticateAsClient(client, testToken, testTimeout); err != nil {
		t.Fatalf("AuthenticateAsClient: %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("AuthenticateAsServer: %v", err)
	}

	for name, conn := range map[string]*deadlineConn{"client": client, "server": server} {
		deadlines := conn.recordedDeadlines()
		if len(deadlines) != 2 {
			t.Fatalf("%s deadline calls = %d, want 2", name, len(deadlines))
		}
		if deadlines[0].IsZero() {
			t.Errorf("%s initial deadline was not set", name)
		}
		if !deadlines[1].IsZero() {
			t.Errorf("%s deadline was not cleared", name)
		}
	}

	writeErr := make(chan error, 1)
	go func() {
		_, err := client.Write([]byte("yamux"))
		writeErr <- err
	}()
	got := make([]byte, len("yamux"))
	if _, err := io.ReadFull(server, got); err != nil {
		t.Fatalf("post-authentication read: %v", err)
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("post-authentication write: %v", err)
	}
	if string(got) != "yamux" {
		t.Fatalf("post-authentication data = %q, want yamux", got)
	}
}

func TestLoadTokenInline(t *testing.T) {
	got, err := LoadToken(" \t"+testToken+"\n", "")
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if got != testToken {
		t.Fatalf("token = %q, want trimmed token", got)
	}
}

func TestLoadTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("\n"+testToken+" \t"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	got, err := LoadToken("", path)
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if got != testToken {
		t.Fatalf("token = %q, want trimmed token", got)
	}
}

func TestLoadTokenRejectsInvalidSources(t *testing.T) {
	oversizedPath := filepath.Join(t.TempDir(), "oversized-token")
	if err := os.WriteFile(oversizedPath, []byte(strings.Repeat("x", maxRecordSize+1)), 0o600); err != nil {
		t.Fatalf("write oversized token file: %v", err)
	}
	shortPath := filepath.Join(t.TempDir(), "short-token")
	if err := os.WriteFile(shortPath, []byte("too-short"), 0o600); err != nil {
		t.Fatalf("write short token file: %v", err)
	}

	tests := []struct {
		name   string
		inline string
		file   string
	}{
		{name: "both set", inline: testToken, file: shortPath},
		{name: "neither set"},
		{name: "short inline", inline: "too-short"},
		{name: "short file", file: shortPath},
		{name: "oversized file", file: oversizedPath},
		{name: "missing file", file: filepath.Join(t.TempDir(), "missing")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadToken(tt.inline, tt.file)
			if err == nil {
				t.Fatal("LoadToken accepted invalid token sources")
			}
			if tt.inline != "" && strings.Contains(err.Error(), tt.inline) {
				t.Fatal("LoadToken error contains inline token")
			}
		})
	}
}

func assertGenericFailureResponse(t *testing.T, conn net.Conn) {
	t.Helper()
	var response authResponse
	if err := readRecord(conn, &response); err != nil {
		t.Fatalf("read authentication response: %v", err)
	}
	if response.Success == nil || *response.Success {
		t.Fatalf("response success = %v, want false", response.Success)
	}
	if response.Error != ErrAuthenticationFailed.Error() {
		t.Fatalf("response error = %q, want %q", response.Error, ErrAuthenticationFailed)
	}
}

type deadlineConn struct {
	net.Conn
	mu        sync.Mutex
	deadlines []time.Time
}

func (c *deadlineConn) SetDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.deadlines = append(c.deadlines, deadline)
	c.mu.Unlock()
	return c.Conn.SetDeadline(deadline)
}

func (c *deadlineConn) recordedDeadlines() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Time(nil), c.deadlines...)
}
