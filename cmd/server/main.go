package main

import (
	"io"
	"log/slog"
	"net"
	"os"

	"ratatosk/internal/tunnel"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	ln, err := net.Listen("tcp", ":7000")
	if err != nil {
		slog.Error("failed to start TCP listener", "error", err)
		os.Exit(1)
	}
	slog.Info("ratatosk relay server listening", "addr", ":7000")

	for {
		conn, err := ln.Accept()
		if err != nil {
			slog.Error("failed to accept connection", "error", err)
			continue
		}
		go handleConnection(conn)
	}
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
	slog.Info("yamux session established", "remote", remote)

	for {
		stream, err := session.Accept()
		if err != nil {
			if err == io.EOF {
				slog.Info("session closed by client", "remote", remote)
			} else {
				slog.Error("failed to accept stream", "remote", remote, "error", err)
			}
			return
		}
		go handleStream(stream, remote)
	}
}

func handleStream(stream net.Conn, remote string) {
	defer stream.Close()

	buf := make([]byte, 1024)
	n, err := stream.Read(buf)
	if err != nil {
		slog.Error("failed to read from stream", "remote", remote, "error", err)
		return
	}

	msg := string(buf[:n])
	slog.Info("received message", "remote", remote, "message", msg)

	if msg == "ping" {
		if _, err := stream.Write([]byte("pong")); err != nil {
			slog.Error("failed to write pong", "remote", remote, "error", err)
			return
		}
		slog.Info("sent pong", "remote", remote)
	}
}
