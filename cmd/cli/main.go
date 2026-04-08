package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"

	"ratatosk/internal/inspector"
	"ratatosk/internal/protocol"
	"ratatosk/internal/tunnel"
	"ratatosk/internal/updater"
)

var Version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Printf("Ratatosk CLI version: %s\n", Version)
			return
		case "self-update":
			if err := updater.UpdateCLI(Version); err != nil {
				slog.Error("update failed", "error", err)
				os.Exit(1)
			}
			return
		}
	}

	port := flag.Int("port", 3000, "local port to expose")
	flag.Parse()
	localAddr := fmt.Sprintf("localhost:%d", *port)

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

	// Open a control stream and perform the handshake.
	controlStream, err := session.Open()
	if err != nil {
		slog.Error("failed to open control stream", "error", err)
		os.Exit(1)
	}

	req := &protocol.TunnelRequest{Protocol: "http", LocalPort: *port}
	if err := protocol.WriteRequest(controlStream, req); err != nil {
		slog.Error("failed to send tunnel request", "error", err)
		os.Exit(1)
	}

	resp, err := protocol.ReadResponse(controlStream)
	if err != nil {
		slog.Error("failed to read tunnel response", "error", err)
		os.Exit(1)
	}
	controlStream.Close()

	if !resp.Success {
		slog.Error("tunnel creation failed", "error", resp.Error)
		os.Exit(1)
	}

	logger := inspector.NewLogger()
	inspectorAddr, inspectorErr := inspector.StartServer(logger)

	fmt.Println()
	fmt.Println("Ratatosk                        (Ctrl+C to quit)")
	fmt.Println()
	fmt.Printf("Forwarding      http://%s.localhost:8080 -> http://localhost:%d\n", resp.Subdomain, *port)
	if inspectorErr != nil {
		slog.Warn("failed to start inspector", "error", inspectorErr)
	} else {
		fmt.Printf("Web Interface   http://%s\n", inspectorAddr)
	}
	fmt.Println()

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
		go handleStream(stream, localAddr, logger)
	}
}

func handleStream(stream net.Conn, localAddr string, logger *inspector.Logger) {
	defer stream.Close()
	inspector.HandleStream(stream, localAddr, logger)
}
