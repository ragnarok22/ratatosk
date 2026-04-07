package main

import (
	"log/slog"
	"net"
	"os"

	"ratatosk/internal/tunnel"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	conn, err := net.Dial("tcp", "localhost:7000")
	if err != nil {
		slog.Error("failed to connect to relay server", "addr", "localhost:7000", "error", err)
		os.Exit(1)
	}
	defer conn.Close()
	slog.Info("connected to relay server", "addr", "localhost:7000")

	session, err := tunnel.NewClientSession(conn)
	if err != nil {
		slog.Error("failed to create yamux session", "error", err)
		os.Exit(1)
	}
	defer session.Close()
	slog.Info("yamux session established")

	stream, err := session.Open()
	if err != nil {
		slog.Error("failed to open stream", "error", err)
		os.Exit(1)
	}
	defer stream.Close()

	if _, err := stream.Write([]byte("ping")); err != nil {
		slog.Error("failed to send ping", "error", err)
		os.Exit(1)
	}
	slog.Info("sent ping")

	buf := make([]byte, 1024)
	n, err := stream.Read(buf)
	if err != nil {
		slog.Error("failed to read response", "error", err)
		os.Exit(1)
	}

	response := string(buf[:n])
	slog.Info("received response", "message", response)

	if response == "pong" {
		slog.Info("ping/pong successful — tunnel multiplexing is working")
	} else {
		slog.Error("unexpected response", "expected", "pong", "got", response)
		os.Exit(1)
	}
}
