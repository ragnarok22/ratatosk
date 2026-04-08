package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"

	"ratatosk/internal/inspector"
	"ratatosk/internal/protocol"
	"ratatosk/internal/redact"
	"ratatosk/internal/tunnel"
	"ratatosk/internal/updater"
)

var Version = "dev"

func main() {
	if code := run(os.Args, os.Getenv, os.Stdout, os.Stderr, updater.UpdateCLI, runClient, runRawClient); code != 0 {
		os.Exit(code)
	}
}

func run(
	args []string,
	getenv func(string) string,
	stdout, stderr io.Writer,
	updateCLI func(string) error,
	runClientFn func(string, int, string) error,
	runRawClientFn func(string, int, string) error,
) int {
	if len(args) > 1 {
		switch args[1] {
		case "version":
			fmt.Fprintf(stdout, "Ratatosk CLI version: %s\n", Version)
			return 0
		case "self-update":
			if err := updateCLI(Version); err != nil {
				slog.Error("update failed", "error", err)
				return 1
			}
			return 0
		case "tcp":
			return runTCPCommand(args[2:], getenv, stdout, stderr, runRawClientFn)
		case "udp":
			return runUDPCommand(args[2:], getenv, stdout, stderr, runRawClientFn)
		}
	}

	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)

	server := flags.String("server", "localhost:7000", "relay server address (host:port)")
	port := flags.Int("port", 3000, "local port to expose")
	streamer := flags.Bool("streamer", false, "redact sensitive data from output for streaming")
	basicAuth := flags.String("basic-auth", "", "require basic auth for tunnel visitors (format: user:pass)")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	if env := getenv("RATATOSK_SERVER"); env != "" && *server == "localhost:7000" {
		*server = env
	}

	if *basicAuth != "" && !strings.Contains(*basicAuth, ":") {
		fmt.Fprintf(stderr, "Error: --basic-auth must be in 'user:pass' format\n")
		return 1
	}

	redact.Enabled = *streamer
	slog.SetDefault(slog.New(redact.NewHandler(slog.NewTextHandler(stdout, nil))))

	if err := runClientFn(*server, *port, *basicAuth); err != nil {
		slog.Error("client error", "error", err)
		return 1
	}
	return 0
}

func runTCPCommand(
	args []string,
	getenv func(string) string,
	stdout, stderr io.Writer,
	runRawClientFn func(string, int, string) error,
) int {
	return runProtoCommand(protocol.ProtoTCP, args, getenv, stdout, stderr, runRawClientFn)
}

func runUDPCommand(
	args []string,
	getenv func(string) string,
	stdout, stderr io.Writer,
	runRawClientFn func(string, int, string) error,
) int {
	return runProtoCommand(protocol.ProtoUDP, args, getenv, stdout, stderr, runRawClientFn)
}

func runProtoCommand(
	proto string,
	args []string,
	getenv func(string) string,
	stdout, stderr io.Writer,
	runRawClientFn func(string, int, string) error,
) int {
	flags := flag.NewFlagSet("ratatosk "+proto, flag.ContinueOnError)
	flags.SetOutput(stderr)
	server := flags.String("server", "localhost:7000", "relay server address (host:port)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	remaining := flags.Args()
	if len(remaining) != 1 {
		fmt.Fprintf(stderr, "Usage: ratatosk %s <port> [--server host:port]\n", proto)
		return 1
	}

	port, err := strconv.Atoi(remaining[0])
	if err != nil || port <= 0 || port > 65535 {
		fmt.Fprintf(stderr, "Error: invalid port %q\n", remaining[0])
		return 1
	}

	if env := getenv("RATATOSK_SERVER"); env != "" && *server == "localhost:7000" {
		*server = env
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(stdout, nil)))

	if err := runRawClientFn(*server, port, proto); err != nil {
		slog.Error("client error", "error", err)
		return 1
	}
	return 0
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

	req := &protocol.TunnelRequest{Protocol: protocol.ProtoHTTP, LocalPort: localPort, BasicAuth: basicAuth}
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

	tunnelURL := resp.URL
	if tunnelURL == "" {
		tunnelURL = fmt.Sprintf("http://%s.localhost:8080", resp.Subdomain)
	}

	fmt.Println()
	fmt.Println("Ratatosk                        (Ctrl+C to quit)")
	fmt.Println()
	if redact.Enabled {
		fmt.Printf("Forwarding      %s -> http://localhost:[REDACTED]\n", tunnelURL)
	} else {
		fmt.Printf("Forwarding      %s -> http://localhost:%d\n", tunnelURL, localPort)
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

func runRawClient(serverAddr string, localPort int, proto string) error {
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

	controlStream, err := session.Open()
	if err != nil {
		return fmt.Errorf("failed to open control stream: %w", err)
	}

	req := &protocol.TunnelRequest{Protocol: proto, LocalPort: localPort}
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

	fmt.Println()
	fmt.Println("Ratatosk                        (Ctrl+C to quit)")
	fmt.Println()
	fmt.Printf("Forwarding      %s:%d -> %s (%s)\n", serverAddr, resp.Port, localAddr, proto)
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
		switch proto {
		case protocol.ProtoTCP:
			go handleTCPStream(stream, localAddr)
		case protocol.ProtoUDP:
			go handleUDPStream(stream, localAddr)
		}
	}
}

func handleTCPStream(stream net.Conn, localAddr string) {
	defer stream.Close()

	local, err := net.Dial("tcp", localAddr)
	if err != nil {
		slog.Error("failed to connect to local service", "addr", localAddr, "error", err)
		return
	}
	defer local.Close()

	done := make(chan struct{})
	go func() {
		io.Copy(local, stream)
		close(done)
	}()
	io.Copy(stream, local)
	<-done
}

func handleUDPStream(stream net.Conn, localAddr string) {
	defer stream.Close()

	udpAddr, err := net.ResolveUDPAddr("udp", localAddr)
	if err != nil {
		slog.Error("failed to resolve UDP address", "addr", localAddr, "error", err)
		return
	}
	local, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		slog.Error("failed to connect to local UDP service", "addr", localAddr, "error", err)
		return
	}
	defer local.Close()

	// stream -> local UDP
	go func() {
		for {
			data, err := tunnel.ReadFrame(stream)
			if err != nil {
				return
			}
			local.Write(data)
		}
	}()

	// local UDP -> stream
	buf := make([]byte, tunnel.MaxUDPFrameSize)
	for {
		n, err := local.Read(buf)
		if err != nil {
			return
		}
		if err := tunnel.WriteFrame(stream, buf[:n]); err != nil {
			return
		}
	}
}
