package inspector

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// HandleStream intercepts HTTP traffic on a yamux stream, logs it, and
// forwards it to the local server at localAddr.
func HandleStream(stream net.Conn, localAddr string, logger *Logger) {
	req, err := http.ReadRequest(bufio.NewReader(stream))
	if err != nil {
		slog.Error("failed to parse HTTP request from stream", "error", err)
		return
	}

	var reqBody []byte
	if req.Body != nil {
		reqBody, err = io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			slog.Error("failed to read request body", "error", err)
			return
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

	start := time.Now()

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		slog.Error("failed to reach local server", "addr", localAddr, "error", err)
		write502(stream, "failed to connect to local server: "+err.Error())
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

	ct := resp.Header.Get("Content-Type")
	binary := isBinaryContentType(ct)

	var loggedRespBody string
	if binary {
		loggedRespBody = base64.StdEncoding.EncodeToString(respBody)
	} else {
		loggedRespBody = TruncateBody(respBody)
	}

	logger.Add(TrafficLog{
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
	})

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
