package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"

	"ratatosk/internal/inspector"
	"ratatosk/internal/protocol"
	"ratatosk/internal/redact"
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
	streamer := flag.Bool("streamer", false, "redact sensitive data from output for streaming")
	basicAuth := flag.String("basic-auth", "", "require basic auth for tunnel visitors (format: user:pass)")
	flag.Parse()

	if *basicAuth != "" && !strings.Contains(*basicAuth, ":") {
		fmt.Fprintf(os.Stderr, "Error: --basic-auth must be in 'user:pass' format\n")
		os.Exit(1)
	}

	redact.Enabled = *streamer
	slog.SetDefault(slog.New(redact.NewHandler(slog.NewTextHandler(os.Stdout, nil))))

	if err := runClient("localhost:7000", *port, *basicAuth); err != nil {
		slog.Error("client error", "error", err)
		os.Exit(1)
	}
}

func runClient(serverAddr string, localPort int, basicAuth string) error {
	localAddr := fmt.Sprintf("localhost:%d", localPort)

	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return fmt.Errorf("failed to connect to relay server at %s: %w", serverAddr, err)
	}
	defer conn.Close()
	slog.Info("connected to relay server", "addr", serverAddr)

	session, err := tunnel.NewClientSession(conn)
	if err != nil {
		return fmt.Errorf("failed to create yamux session: %w", err)
	}
	defer session.Close()

	// Open a control stream and perform the handshake.
	controlStream, err := session.Open()
	if err != nil {
		return fmt.Errorf("failed to open control stream: %w", err)
	}

	req := &protocol.TunnelRequest{Protocol: "http", LocalPort: localPort, BasicAuth: basicAuth}
	if err := protocol.WriteRequest(controlStream, req); err != nil {
		return fmt.Errorf("failed to send tunnel request: %w", err)
	}

	resp, err := protocol.ReadResponse(controlStream)
	if err != nil {
		return fmt.Errorf("failed to read tunnel response: %w", err)
	}
	controlStream.Close()

	if !resp.Success {
		return fmt.Errorf("tunnel creation failed: %s", resp.Error)
	}

	logger := inspector.NewLogger()
	inspectorAddr, inspectorErr := inspector.StartServer(logger)

	fmt.Println()
	fmt.Println("Ratatosk                        (Ctrl+C to quit)")
	fmt.Println()
	if redact.Enabled {
		fmt.Printf("Forwarding      http://%s.localhost:8080 -> http://localhost:[REDACTED]\n", resp.Subdomain)
	} else {
		fmt.Printf("Forwarding      http://%s.localhost:8080 -> http://localhost:%d\n", resp.Subdomain, localPort)
	}
	if basicAuth != "" {
		fmt.Printf("Basic Auth      enabled (user: %s)\n", strings.SplitN(basicAuth, ":", 2)[0])
	}
	if inspectorErr != nil {
		slog.Warn("failed to start inspector", "error", inspectorErr)
	} else if redact.Enabled {
		fmt.Printf("Web Interface   http://[REDACTED]\n")
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
			return nil
		}
		go handleStream(stream, localAddr, logger)
	}
}

func handleStream(stream net.Conn, localAddr string, logger *inspector.Logger) {
	defer stream.Close()
	inspector.HandleStream(stream, localAddr, logger)
}
