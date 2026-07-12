package inspector

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleStream(t *testing.T) {
	// Start a local HTTP server to act as the user's app.
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "hello")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "response-body")
	}))
	defer local.Close()

	// local.URL is "http://127.0.0.1:PORT" — extract host:port.
	localAddr := strings.TrimPrefix(local.URL, "http://")

	logger := NewLogger()

	// Create an in-memory connection pair to simulate a yamux stream.
	clientConn, serverConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		HandleStream(serverConn, localAddr, logger)
		serverConn.Close()
	}()

	// Write a raw HTTP request into the pipe.
	rawReq := "GET /test-path HTTP/1.1\r\nHost: example.com\r\nContent-Length: 0\r\n\r\n"
	_, err := clientConn.Write([]byte(rawReq))
	if err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	// Read the HTTP response from the pipe.
	resp, err := http.ReadResponse(bufio.NewReader(clientConn), nil)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	defer resp.Body.Close()
	clientConn.Close()

	<-done

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	entries := logger.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(entries))
	}

	e := entries[0]
	if e.Method != "GET" {
		t.Errorf("expected method GET, got %s", e.Method)
	}
	if e.Path != "/test-path" {
		t.Errorf("expected path /test-path, got %s", e.Path)
	}
	if e.RespStatus != 200 {
		t.Errorf("expected resp status 200, got %d", e.RespStatus)
	}
	if e.RespBody != "response-body" {
		t.Errorf("expected resp body 'response-body', got %q", e.RespBody)
	}
	if e.Duration <= 0 {
		t.Error("expected positive duration")
	}
}

func TestHandleStreamLocalServerDown(t *testing.T) {
	logger := NewLogger()

	clientConn, serverConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Use an address with no server listening.
		HandleStream(serverConn, "127.0.0.1:1", logger)
		serverConn.Close()
	}()

	rawReq := "GET / HTTP/1.1\r\nHost: example.com\r\nContent-Length: 0\r\n\r\n"
	clientConn.Write([]byte(rawReq))

	resp, err := http.ReadResponse(bufio.NewReader(clientConn), nil)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read 502 body: %v", err)
	}
	if string(body) != "Bad Gateway\n" {
		t.Fatalf("502 body = %q, want generic message", body)
	}
	clientConn.Close()
	<-done
}

func TestHandleStreamBinaryResponse(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0x89, 0x50, 0x4E, 0x47})
	}))
	defer local.Close()
	localAddr := strings.TrimPrefix(local.URL, "http://")

	logger := NewLogger()
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		HandleStream(serverConn, localAddr, logger)
		serverConn.Close()
	}()

	clientConn.Write([]byte("GET /image.png HTTP/1.1\r\nHost: example.com\r\nContent-Length: 0\r\n\r\n"))

	resp, err := http.ReadResponse(bufio.NewReader(clientConn), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	resp.Body.Close()
	clientConn.Close()
	<-done

	entries := logger.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].RespBodyBinary {
		t.Error("expected RespBodyBinary to be true for image/png")
	}
}

func TestHandleStreamBadRequest(t *testing.T) {
	logger := NewLogger()
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		HandleStream(serverConn, "127.0.0.1:1", logger)
		serverConn.Close()
	}()

	clientConn.Write([]byte("not an http request\r\n\r\n"))
	clientConn.Close()
	<-done

	if len(logger.Entries()) != 0 {
		t.Errorf("expected 0 entries for bad request, got %d", len(logger.Entries()))
	}
}

func TestFlattenHeaders(t *testing.T) {
	h := http.Header{
		"Content-Type": {"application/json"},
		"Accept":       {"text/html", "application/json"},
	}

	flat := flattenHeaders(h)
	if flat["Content-Type"] != "application/json" {
		t.Errorf("unexpected Content-Type: %q", flat["Content-Type"])
	}
	if flat["Accept"] != "text/html, application/json" {
		t.Errorf("unexpected Accept: %q", flat["Accept"])
	}
}

func TestHandleStreamForwardsStreamingResponse(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "first")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "second")
	}))
	defer local.Close()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan struct{})
	go func() {
		HandleStream(serverConn, strings.TrimPrefix(local.URL, "http://"), NewLogger())
		serverConn.Close()
		close(done)
	}()

	if _, err := io.WriteString(clientConn, "GET /stream HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}

	response := make(chan *http.Response, 1)
	errs := make(chan error, 1)
	go func() {
		resp, err := http.ReadResponse(bufio.NewReader(clientConn), nil)
		if err != nil {
			errs <- err
			return
		}
		response <- resp
	}()

	var resp *http.Response
	select {
	case resp = <-response:
	case err := <-errs:
		t.Fatalf("ReadResponse: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("response headers were not forwarded before the streaming body completed")
	}

	close(release)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	resp.Body.Close()
	if string(body) != "firstsecond" {
		t.Fatalf("body = %q, want %q", body, "firstsecond")
	}
	<-done
}

func TestBuildTrafficLogCapsBinaryResponse(t *testing.T) {
	body := bytes.Repeat([]byte{0xff}, maxBodyLog+1)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/image", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/png"}}}

	entry := buildTrafficLog(req, nil, resp, body, time.Now(), time.Second)
	decoded, err := base64.StdEncoding.DecodeString(entry.RespBody)
	if err != nil {
		t.Fatalf("DecodeString: %v", err)
	}
	if len(decoded) != maxBodyLog {
		t.Fatalf("logged binary body length = %d, want %d", len(decoded), maxBodyLog)
	}
}

func TestHandleStreamForwardsStreamingRequest(t *testing.T) {
	receivedPrefix := make(chan struct{})
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := make([]byte, 5)
		if _, err := io.ReadFull(r.Body, prefix); err != nil {
			t.Errorf("read request prefix: %v", err)
			return
		}
		if string(prefix) != "first" {
			t.Errorf("request prefix = %q, want first", prefix)
		}
		close(receivedPrefix)
		rest, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request remainder: %v", err)
			return
		}
		_, _ = io.WriteString(w, string(prefix)+string(rest))
	}))
	defer local.Close()

	logger := NewLogger()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan struct{})
	go func() {
		HandleStream(serverConn, strings.TrimPrefix(local.URL, "http://"), logger)
		serverConn.Close()
		close(done)
	}()

	if _, err := io.WriteString(clientConn, "POST /stream HTTP/1.1\r\nHost: example.com\r\nContent-Length: 11\r\n\r\nfirst"); err != nil {
		t.Fatalf("write request prefix: %v", err)
	}
	select {
	case <-receivedPrefix:
	case <-time.After(2 * time.Second):
		t.Fatal("request prefix was not forwarded before the full body arrived")
	}
	if _, err := io.WriteString(clientConn, "second"); err != nil {
		t.Fatalf("write request remainder: %v", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(clientConn), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()
	if string(body) != "firstsecond" {
		t.Fatalf("response body = %q, want firstsecond", body)
	}
	<-done

	entries := logger.Entries()
	if len(entries) != 1 || entries[0].ReqBody != "firstsecond" {
		t.Fatalf("logged request body = %+v, want firstsecond", entries)
	}
}

func TestHandleStreamProxiesUpgrade(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("response writer does not support hijacking")
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
		_ = rw.Flush()
		payload := make([]byte, 4)
		if _, err := io.ReadFull(rw, payload); err != nil {
			t.Errorf("read upgraded payload: %v", err)
			return
		}
		_, _ = rw.Write(payload)
		_ = rw.Flush()
	}))
	defer local.Close()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan struct{})
	go func() {
		HandleStream(serverConn, strings.TrimPrefix(local.URL, "http://"), NewLogger())
		serverConn.Close()
		close(done)
	}()

	request := "GET /upgrade HTTP/1.1\r\nHost: example.com\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n"
	if _, err := io.WriteString(clientConn, request); err != nil {
		t.Fatalf("write upgrade request: %v", err)
	}
	reader := bufio.NewReader(clientConn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	if _, err := io.WriteString(clientConn, "ping"); err != nil {
		t.Fatalf("write upgraded payload: %v", err)
	}
	payload := make([]byte, 4)
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatalf("read upgraded payload: %v", err)
	}
	if string(payload) != "ping" {
		t.Fatalf("upgraded payload = %q, want ping", payload)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("upgrade proxy did not finish after the local connection closed")
	}
}
