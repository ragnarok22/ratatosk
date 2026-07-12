package inspector

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type boundedCapture struct {
	mu   sync.Mutex
	body []byte
}

func (c *boundedCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	remaining := maxBodyLog - len(c.body)
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > 0 {
		c.body = append(c.body, p[:remaining]...)
	}
	return len(p), nil
}

func (c *boundedCapture) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]byte(nil), c.body...)
}

type capturingReadCloser struct {
	io.ReadCloser
	capture *boundedCapture
}

func (r *capturingReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		_, _ = r.capture.Write(p[:n])
	}
	return n, err
}

// parseAndPrepareRequest parses the request without draining its body. The
// transport captures a bounded prefix while streaming the body to the app.
func parseAndPrepareRequest(stream net.Conn, localAddr string) (*http.Request, *bufio.Reader, *boundedCapture, error) {
	streamReader := bufio.NewReader(stream)
	req, err := http.ReadRequest(streamReader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to parse HTTP request from stream: %w", err)
	}

	reqBody := &boundedCapture{}
	if req.Body != nil && req.Body != http.NoBody {
		req.Body = &capturingReadCloser{ReadCloser: req.Body, capture: reqBody}
	}

	req.URL.Scheme = "http"
	req.URL.Host = localAddr
	req.RequestURI = ""

	// Keep logged response bodies readable and avoid transparent decompression.
	req.Header.Del("Accept-Encoding")

	return req, streamReader, reqBody, nil
}

// buildTrafficLog creates a TrafficLog entry from request/response data.
func buildTrafficLog(req *http.Request, reqBody []byte, resp *http.Response, respBody []byte, start time.Time, duration time.Duration) TrafficLog {
	ct := resp.Header.Get("Content-Type")
	binary := isBinaryContentType(ct)
	if len(respBody) > maxBodyLog {
		respBody = respBody[:maxBodyLog]
	}

	var loggedRespBody string
	if binary {
		loggedRespBody = base64.StdEncoding.EncodeToString(respBody)
	} else {
		loggedRespBody = string(respBody)
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
	req, streamReader, reqBody, err := parseAndPrepareRequest(stream, localAddr)
	if err != nil {
		slog.Error(err.Error())
		return
	}

	start := time.Now()
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		slog.Error("failed to reach local server", "addr", localAddr, "error", err)
		if writeErr := write502(stream); writeErr != nil {
			slog.Error("failed to write bad gateway response", "error", writeErr)
		}
		return
	}

	respBody := &boundedCapture{}
	var transferErr error
	if resp.StatusCode == http.StatusSwitchingProtocols {
		transferErr = proxyUpgrade(stream, streamReader, req, resp)
	} else {
		transferErr = proxyResponse(stream, req, resp, respBody)
		if closeErr := resp.Body.Close(); closeErr != nil {
			transferErr = errors.Join(transferErr, fmt.Errorf("close local response body: %w", closeErr))
		}
	}

	duration := time.Since(start)
	logger.Add(buildTrafficLog(req, reqBody.Bytes(), resp, respBody.Bytes(), start, duration))

	if transferErr != nil {
		slog.Error("failed to transfer local response", "error", transferErr)
		return
	}
	slog.Info("request completed",
		"method", req.Method,
		"path", req.URL.Path,
		"status", resp.StatusCode,
		"duration", duration,
	)
}

func proxyResponse(stream net.Conn, req *http.Request, resp *http.Response, capture *boundedCapture) error {
	chunked, hasBody, err := writeResponseHead(stream, req, resp)
	if err != nil {
		return err
	}
	if !hasBody {
		return nil
	}

	body := io.TeeReader(resp.Body, capture)
	if !chunked {
		_, err = io.Copy(stream, body)
		return err
	}

	w := &chunkedResponseWriter{writer: bufio.NewWriter(stream)}
	if _, err = io.Copy(w, body); err != nil {
		return err
	}
	return w.Close(resp.Trailer)
}

func proxyUpgrade(stream net.Conn, streamReader *bufio.Reader, req *http.Request, resp *http.Response) error {
	if _, _, err := writeResponseHead(stream, req, resp); err != nil {
		_ = resp.Body.Close()
		return err
	}

	upstream, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		_ = resp.Body.Close()
		return errors.New("local upgrade response is not bidirectional")
	}

	requestDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(upstream, streamReader)
		if closer, ok := upstream.(interface{ CloseWrite() error }); ok {
			copyErr = errors.Join(copyErr, closer.CloseWrite())
		}
		requestDone <- copyErr
	}()

	_, responseErr := io.Copy(stream, upstream)
	if closer, ok := stream.(interface{ CloseWrite() error }); ok {
		responseErr = errors.Join(responseErr, closer.CloseWrite())
	}
	_ = upstream.Close()
	_ = stream.Close()
	requestErr := <-requestDone
	if errors.Is(responseErr, net.ErrClosed) || errors.Is(responseErr, io.ErrClosedPipe) {
		responseErr = nil
	}
	if errors.Is(requestErr, net.ErrClosed) || errors.Is(requestErr, io.ErrClosedPipe) {
		requestErr = nil
	}
	return errors.Join(responseErr, requestErr)
}

func writeResponseHead(stream io.Writer, req *http.Request, resp *http.Response) (chunked bool, hasBody bool, err error) {
	headers := resp.Header.Clone()
	headers.Del("Content-Length")
	headers.Del("Transfer-Encoding")

	hasBody = req.Method != http.MethodHead && resp.StatusCode != http.StatusSwitchingProtocols &&
		resp.StatusCode >= 200 && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified
	if hasBody {
		if resp.ContentLength >= 0 {
			headers.Set("Content-Length", fmt.Sprintf("%d", resp.ContentLength))
		} else {
			chunked = true
			headers.Set("Transfer-Encoding", "chunked")
			if len(resp.Trailer) > 0 {
				names := make([]string, 0, len(resp.Trailer))
				for name := range resp.Trailer {
					names = append(names, name)
				}
				sort.Strings(names)
				headers.Set("Trailer", strings.Join(names, ", "))
			}
		}
	} else if req.Method == http.MethodHead && resp.ContentLength >= 0 {
		headers.Set("Content-Length", fmt.Sprintf("%d", resp.ContentLength))
	}

	writer := bufio.NewWriter(stream)
	statusText := http.StatusText(resp.StatusCode)
	if statusText == "" {
		statusText = "status code"
	}
	if _, err = fmt.Fprintf(writer, "HTTP/1.1 %03d %s\r\n", resp.StatusCode, statusText); err != nil {
		return false, false, fmt.Errorf("write response status: %w", err)
	}
	if err = headers.Write(writer); err != nil {
		return false, false, fmt.Errorf("write response headers: %w", err)
	}
	if _, err = writer.WriteString("\r\n"); err != nil {
		return false, false, fmt.Errorf("finish response headers: %w", err)
	}
	if err = writer.Flush(); err != nil {
		return false, false, fmt.Errorf("flush response headers: %w", err)
	}
	return chunked, hasBody, nil
}

type chunkedResponseWriter struct {
	writer *bufio.Writer
}

func (w *chunkedResponseWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if _, err := fmt.Fprintf(w.writer, "%x\r\n", len(p)); err != nil {
		return 0, err
	}
	n, err := w.writer.Write(p)
	if err != nil {
		return n, err
	}
	if _, err = w.writer.WriteString("\r\n"); err != nil {
		return n, err
	}
	if err = w.writer.Flush(); err != nil {
		return n, err
	}
	return n, nil
}

func (w *chunkedResponseWriter) Close(trailers http.Header) error {
	if _, err := w.writer.WriteString("0\r\n"); err != nil {
		return err
	}
	if err := trailers.Write(w.writer); err != nil {
		return err
	}
	if _, err := w.writer.WriteString("\r\n"); err != nil {
		return err
	}
	return w.writer.Flush()
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

func write502(w io.Writer) error {
	const body = "Bad Gateway\n"
	resp := &http.Response{
		StatusCode:    http.StatusBadGateway,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
	return resp.Write(w)
}
