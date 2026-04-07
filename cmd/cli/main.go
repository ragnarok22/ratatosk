package main

import (
	"io"
	"log/slog"
	"net"
	"os"
	"sync"

	"ratatosk/internal/tunnel"
)

const localAddr = "localhost:3000"

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
	slog.Info("yamux session established, waiting for tunnel requests", "forward", localAddr)

	for {
		stream, err := session.Accept()
		if err != nil {
			if err == io.EOF {
				slog.Info("session closed by server")
			} else {
				slog.Error("session error", "error", err)
			}
			return
		}
		go handleStream(stream)
	}
}

func handleStream(stream net.Conn) {
	defer stream.Close()

	local, err := net.Dial("tcp", localAddr)
	if err != nil {
		slog.Error("failed to connect to local server", "addr", localAddr, "error", err)
		return
	}
	defer local.Close()

	slog.Info("forwarding request", "addr", localAddr)

	var wg sync.WaitGroup
	wg.Add(2)

	// Forward HTTP request from tunnel to local server.
	go func() {
		defer wg.Done()
		io.Copy(local, stream)
	}()

	// Forward HTTP response from local server back through tunnel.
	go func() {
		defer wg.Done()
		io.Copy(stream, local)
	}()

	wg.Wait()
	slog.Info("request completed")
}
