package inspector

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"ratatosk/internal/redact"
)

// parseAndPrepareRequest reads an HTTP request from the stream, drains
// its body, and rewrites the URL so it can be forwarded via
// http.DefaultTransport. It returns the parsed request and the raw
// request body bytes.
func parseAndPrepareRequest(stream net.Conn, localAddr string) (*http.Request, []byte, error) {
	req, err := http.ReadRequest(bufio.NewReader(stream))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse HTTP request from stream: %w", err)
	}

	var reqBody []byte
	if req.Body != nil {
		reqBody, err = io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read request body: %w", err)
		}
	}

	// Reconstruct body so it can be forwarded.
	req.Body = io.NopCloser(bytes.NewReader(reqBody))
	req.ContentLength = int64(len(reqBody))

	// Prepare the request for http.Transport.
	req.URL.Scheme = "http"
	req.URL.Host = localAddr
	req.RequestURI = ""

	// Remove Accept-Encoding so the local server responds uncompressed.
	// This ensures logged bodies are human-readable.
	req.Header.Del("Accept-Encoding")

	return req, reqBody, nil
}

// buildTrafficLog creates a TrafficLog entry from request/response data.
func buildTrafficLog(req *http.Request, reqBody []byte, resp *http.Response, respBody []byte, start time.Time, duration time.Duration) TrafficLog {
	ct := resp.Header.Get("Content-Type")
	binary := isBinaryContentType(ct)

	var loggedRespBody string
	if binary {
		loggedRespBody = base64.StdEncoding.EncodeToString(respBody)
	} else {
		loggedRespBody = TruncateBody(respBody)
	}

	return TrafficLog{
		Method:         req.Method,
		Path:           req.URL.Path,
		ReqHeaders:     flattenHeaders(req.Header),
		ReqBody:        TruncateBody(reqBody),
		RespStatus:     resp.StatusCode,
		RespHeaders:    flattenHeaders(resp.Header),
		RespBody:       loggedRespBody,
		RespBodyBinary: binary,
		Duration:       duration,
		Timestamp:      start,
	}
}

// HandleStream intercepts HTTP traffic on a yamux stream, logs it, and
// forwards it to the local server at localAddr.
func HandleStream(stream net.Conn, localAddr string, logger *Logger) {
	req, reqBody, err := parseAndPrepareRequest(stream, localAddr)
	if err != nil {
		slog.Error(err.Error())
		return
	}

	start := time.Now()

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		slog.Error("failed to reach local server", "addr", localAddr, "error", err)
		write502(stream, redact.String("failed to connect to local server: "+err.Error()))
		return
	}

	var respBody []byte
	if resp.Body != nil {
		respBody, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			slog.Error("failed to read response body", "error", err)
			return
		}
	}

	duration := time.Since(start)

	logger.Add(buildTrafficLog(req, reqBody, resp, respBody, start, duration))

	slog.Info("request completed",
		"method", req.Method,
		"path", req.URL.Path,
		"status", resp.StatusCode,
		"duration", duration,
	)

	// Write response back to the yamux stream in HTTP/1.1 wire format.
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	resp.ContentLength = int64(len(respBody))
	resp.Write(stream)
}

func isBinaryContentType(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "image/") ||
		strings.HasPrefix(ct, "audio/") ||
		strings.HasPrefix(ct, "video/") ||
		strings.HasPrefix(ct, "application/octet-stream")
}

func flattenHeaders(h http.Header) map[string]string {
	flat := make(map[string]string, len(h))
	for k, v := range h {
		flat[k] = strings.Join(v, ", ")
	}
	return flat
}

func write502(w io.Writer, msg string) {
	resp := &http.Response{
		StatusCode:    http.StatusBadGateway,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": {"text/plain"}},
		Body:          io.NopCloser(strings.NewReader(msg)),
		ContentLength: int64(len(msg)),
	}
	resp.Write(w)
}
