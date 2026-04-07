package main

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"ratatosk/internal/tunnel"
)

var registry = tunnel.NewRegistry()

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	// Start the TCP control plane listener in a goroutine.
	go func() {
		ln, err := net.Listen("tcp", ":7000")
		if err != nil {
			slog.Error("failed to start TCP listener", "error", err)
			os.Exit(1)
		}
		slog.Info("control plane listening", "addr", ":7000")

		for {
			conn, err := ln.Accept()
			if err != nil {
				slog.Error("failed to accept connection", "error", err)
				continue
			}
			go handleConnection(conn)
		}
	}()

	// Start the public HTTP server on the main goroutine.
	slog.Info("public HTTP server listening", "addr", ":8080")
	if err := http.ListenAndServe(":8080", http.HandlerFunc(handleHTTP)); err != nil {
		slog.Error("HTTP server failed", "error", err)
		os.Exit(1)
	}
}

func generateSubdomain() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func handleConnection(conn net.Conn) {
	remote := conn.RemoteAddr().String()
	slog.Info("new TCP connection", "remote", remote)

	session, err := tunnel.NewServerSession(conn)
	if err != nil {
		slog.Error("failed to create yamux session", "remote", remote, "error", err)
		conn.Close()
		return
	}
	defer session.Close()

	subdomain, err := generateSubdomain()
	if err != nil {
		slog.Error("failed to generate subdomain", "error", err)
		return
	}

	registry.Register(subdomain, session)
	slog.Info("tunnel registered",
		"subdomain", subdomain,
		"url", "http://"+subdomain+".localhost:8080",
		"remote", remote,
	)

	// Block until the client disconnects. The server opens streams to the
	// client (in handleHTTP), so Accept here only serves as a sentinel.
	for {
		stream, err := session.Accept()
		if err != nil {
			if err == io.EOF {
				slog.Info("client disconnected", "subdomain", subdomain, "remote", remote)
			} else {
				slog.Warn("session error", "subdomain", subdomain, "remote", remote, "error", err)
			}
			break
		}
		stream.Close()
	}

	registry.Unregister(subdomain)
	slog.Info("tunnel unregistered", "subdomain", subdomain, "remote", remote)
}

func handleHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract subdomain from Host header (e.g. "a1b2c3.localhost:8080").
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	parts := strings.SplitN(host, ".", 2)
	if len(parts) < 2 {
		http.Error(w, "invalid host", http.StatusBadRequest)
		return
	}
	subdomain := parts[0]

	session, ok := registry.GetSession(subdomain)
	if !ok {
		http.Error(w, "tunnel not found", http.StatusBadGateway)
		return
	}

	// Open a yamux stream to the CLI client.
	stream, err := session.Open()
	if err != nil {
		slog.Error("failed to open stream", "subdomain", subdomain, "error", err)
		http.Error(w, "tunnel unavailable", http.StatusBadGateway)
		return
	}

	// Hijack the public-facing HTTP connection to get the raw net.Conn.
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		slog.Error("hijacking not supported")
		stream.Close()
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		slog.Error("hijack failed", "error", err)
		stream.Close()
		return
	}

	// Write the original HTTP request in wire format into the yamux stream.
	if err := r.Write(stream); err != nil {
		slog.Error("failed to write request to stream", "subdomain", subdomain, "error", err)
		stream.Close()
		clientConn.Close()
		return
	}

	slog.Info("proxying request", "subdomain", subdomain, "method", r.Method, "path", r.URL.Path)

	// Bidirectional pipe between the browser and the yamux stream.
	var wg sync.WaitGroup
	wg.Add(2)

	// Response: stream → browser
	go func() {
		defer wg.Done()
		io.Copy(clientConn, stream)
	}()

	// Remaining client data: browser → stream
	go func() {
		defer wg.Done()
		io.Copy(stream, clientConn)
	}()

	wg.Wait()
	stream.Close()
	clientConn.Close()
}
