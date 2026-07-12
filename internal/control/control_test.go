package control

import (
	"bytes"
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

func TestAuthenticationRejectsNilConnections(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "client", run: func() error { return AuthenticateAsClient(nil, testToken, testTimeout) }},
		{name: "server", run: func() error { return AuthenticateAsServer(nil, testToken, testTimeout) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || err.Error() != "control connection is nil" {
				t.Fatalf("error = %v, want nil connection error", err)
			}
			assertErrorOmitsTokens(t, err, testToken)
		})
	}
}

func TestAuthenticationReportsDeadlineSetupFailures(t *testing.T) {
	deadlineErr := errors.New("deadline unavailable")
	tests := []struct {
		name string
		run  func(net.Conn) error
	}{
		{name: "client", run: func(conn net.Conn) error {
			return AuthenticateAsClient(conn, testToken, testTimeout)
		}},
		{name: "server", run: func(conn net.Conn) error {
			return AuthenticateAsServer(conn, testToken, testTimeout)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &faultConn{
				setDeadline: func(time.Time) error { return deadlineErr },
			}
			err := tt.run(conn)
			if !errors.Is(err, deadlineErr) || !strings.Contains(err.Error(), "set authentication deadline") {
				t.Fatalf("error = %v, want wrapped deadline setup error", err)
			}
			assertErrorOmitsTokens(t, err, testToken)
		})
	}
}

func TestAuthenticationReportsDeadlineClearFailures(t *testing.T) {
	deadlineErr := errors.New("deadline clear unavailable")
	for _, failingSide := range []string{"client", "server"} {
		t.Run(failingSide, func(t *testing.T) {
			clientPipe, serverPipe := net.Pipe()
			defer clientPipe.Close()
			defer serverPipe.Close()

			var client net.Conn = clientPipe
			var server net.Conn = serverPipe
			if failingSide == "client" {
				client = &failDeadlineConn{Conn: clientPipe, failCall: 2, err: deadlineErr}
			} else {
				server = &failDeadlineConn{Conn: serverPipe, failCall: 2, err: deadlineErr}
			}

			serverErr := make(chan error, 1)
			go func() {
				serverErr <- AuthenticateAsServer(server, testToken, testTimeout)
			}()
			clientErr := AuthenticateAsClient(client, testToken, testTimeout)
			gotServerErr := <-serverErr

			failedErr, otherErr := clientErr, gotServerErr
			if failingSide == "server" {
				failedErr, otherErr = gotServerErr, clientErr
			}
			if !errors.Is(failedErr, deadlineErr) || !strings.Contains(failedErr.Error(), "clear authentication deadline") {
				t.Fatalf("%s error = %v, want wrapped deadline clear error", failingSide, failedErr)
			}
			if otherErr != nil {
				t.Fatalf("other peer error = %v, want nil", otherErr)
			}
			assertErrorOmitsTokens(t, failedErr, testToken)
		})
	}
}

func TestClientReportsConnectionFailures(t *testing.T) {
	writeErr := errors.New("request write failed")
	readErr := errors.New("response read failed")
	tests := []struct {
		name  string
		conn  net.Conn
		want  error
		phase string
	}{
		{
			name:  "write request",
			conn:  &faultConn{write: func([]byte) (int, error) { return 0, writeErr }},
			want:  writeErr,
			phase: "send authentication request",
		},
		{
			name: "read response",
			conn: &faultConn{
				write: func(p []byte) (int, error) { return len(p), nil },
				read:  func([]byte) (int, error) { return 0, readErr },
			},
			want:  readErr,
			phase: "read authentication response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AuthenticateAsClient(tt.conn, testToken, testTimeout)
			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), tt.phase) {
				t.Fatalf("error = %v, want wrapped %s error", err, tt.phase)
			}
			assertErrorOmitsTokens(t, err, testToken)
		})
	}
}

func TestServerReportsResponseWriteFailures(t *testing.T) {
	writeErr := errors.New("response write failed")
	tests := []struct {
		name    string
		request string
	}{
		{name: "success response", request: `{"version":1,"token":"` + testToken + `"}` + "\n"},
		{name: "rejection response", request: "{\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &faultConn{
				read:  strings.NewReader(tt.request).Read,
				write: func([]byte) (int, error) { return 0, writeErr },
			}
			err := AuthenticateAsServer(conn, testToken, testTimeout)
			if !errors.Is(err, writeErr) || !strings.Contains(err.Error(), "send authentication response") {
				t.Fatalf("error = %v, want wrapped response write error", err)
			}
			assertErrorOmitsTokens(t, err, testToken)
		})
	}
}

func TestServerRejectsReadFailureGenerically(t *testing.T) {
	readErr := errors.New("request read failed")
	var written bytes.Buffer
	conn := &faultConn{
		read:  func([]byte) (int, error) { return 0, readErr },
		write: written.Write,
	}

	err := AuthenticateAsServer(conn, testToken, testTimeout)
	if !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("error = %v, want ErrAuthenticationFailed", err)
	}
	if errors.Is(err, readErr) {
		t.Fatalf("error exposes request read failure: %v", err)
	}
	assertErrorOmitsTokens(t, err, testToken)

	var response authResponse
	if err := readRecord(&written, &response); err != nil {
		t.Fatalf("read rejection response: %v", err)
	}
	if response.Success == nil || *response.Success || response.Error != ErrAuthenticationFailed.Error() {
		t.Fatalf("response = %+v, want generic authentication failure", response)
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
		{name: "malformed JSON", response: `{"success":` + "\n"},
		{name: "oversized", response: strings.Repeat("x", maxRecordSize+1) + "\n"},
		{name: "missing success", response: `{"error":"authentication failed"}` + "\n"},
		{name: "missing failure error", response: `{"success":false}` + "\n"},
		{name: "unexpected failure error", response: `{"success":false,"error":"try again"}` + "\n"},
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
			assertErrorOmitsTokens(t, err, testToken)
			client.Close()
			server.Close()
			<-peerErr
		})
	}
}

func TestReadRecordFailures(t *testing.T) {
	readErr := errors.New("record read failed")
	tests := []struct {
		name   string
		reader io.Reader
		want   string
		cause  error
	}{
		{name: "unterminated", reader: strings.NewReader(`{"success":true}`), want: "authentication record is not newline terminated"},
		{name: "read error", reader: errorReader{err: readErr}, want: "read authentication record", cause: readErr},
		{name: "no progress", reader: zeroReader{}, want: "read authentication record", cause: io.ErrNoProgress},
		{name: "trailing data", reader: strings.NewReader(`{"success":true}garbage` + "\n"), want: "authentication record contains trailing data"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var response authResponse
			err := readRecord(tt.reader, &response)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if tt.cause != nil && !errors.Is(err, tt.cause) {
				t.Fatalf("error = %v, want cause %v", err, tt.cause)
			}
		})
	}
}

func TestWriteRecordFailures(t *testing.T) {
	writeErr := errors.New("record write failed")
	tests := []struct {
		name  string
		write func() error
		want  string
		cause error
	}{
		{
			name:  "encode",
			write: func() error { return writeRecord(io.Discard, make(chan int)) },
			want:  "encode authentication record",
		},
		{
			name: "oversized",
			write: func() error {
				return writeRecord(io.Discard, strings.Repeat("x", maxRecordSize+1))
			},
			want: "authentication record exceeds",
		},
		{
			name:  "write error",
			write: func() error { return writeRecord(errorWriter{err: writeErr}, authResponse{}) },
			want:  "write authentication record",
			cause: writeErr,
		},
		{
			name:  "short write",
			write: func() error { return writeRecord(shortWriter{}, authResponse{}) },
			want:  io.ErrShortWrite.Error(),
			cause: io.ErrShortWrite,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.write()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if tt.cause != nil && !errors.Is(err, tt.cause) {
				t.Fatalf("error = %v, want cause %v", err, tt.cause)
			}
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
		secret string
		want   string
	}{
		{name: "both set", inline: testToken, file: shortPath, secret: testToken, want: "control token sources are mutually exclusive"},
		{name: "neither set", want: "control token is required"},
		{name: "short inline", inline: "too-short", secret: "too-short", want: "control token must be at least"},
		{name: "short file", file: shortPath, secret: "too-short", want: "control token must be at least"},
		{name: "oversized file", file: oversizedPath, secret: strings.Repeat("x", maxRecordSize+1), want: "control token file exceeds"},
		{name: "missing file", file: filepath.Join(t.TempDir(), "missing"), want: "open control token file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadToken(tt.inline, tt.file)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			assertErrorOmitsTokens(t, err, tt.secret)
		})
	}
}

func TestLoadTokenReportsFileReadFailure(t *testing.T) {
	_, err := LoadToken("", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "read control token file") {
		t.Fatalf("error = %v, want token file read error", err)
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

func assertErrorOmitsTokens(t *testing.T, err error, tokens ...string) {
	t.Helper()
	for _, token := range tokens {
		if token != "" && strings.Contains(err.Error(), token) {
			t.Fatalf("error contains token %q: %v", token, err)
		}
	}
}

type faultConn struct {
	net.Conn
	read        func([]byte) (int, error)
	write       func([]byte) (int, error)
	setDeadline func(time.Time) error
}

func (c *faultConn) Read(p []byte) (int, error) {
	if c.read == nil {
		return 0, io.EOF
	}
	return c.read(p)
}

func (c *faultConn) Write(p []byte) (int, error) {
	if c.write == nil {
		return len(p), nil
	}
	return c.write(p)
}

func (c *faultConn) SetDeadline(deadline time.Time) error {
	if c.setDeadline == nil {
		return nil
	}
	return c.setDeadline(deadline)
}

type failDeadlineConn struct {
	net.Conn
	mu       sync.Mutex
	calls    int
	failCall int
	err      error
}

func (c *failDeadlineConn) SetDeadline(deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls == c.failCall {
		return c.err
	}
	return c.Conn.SetDeadline(deadline)
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

type zeroReader struct{}

func (zeroReader) Read([]byte) (int, error) {
	return 0, nil
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	return len(p) - 1, nil
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
