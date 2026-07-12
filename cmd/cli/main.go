package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"ratatosk/internal/control"
	"ratatosk/internal/inspector"
	"ratatosk/internal/protocol"
	"ratatosk/internal/redact"
	"ratatosk/internal/tunnel"
	"ratatosk/internal/updater"

	"github.com/hashicorp/yamux"
)

var Version = "dev"

type controlOptions struct {
	Token      string
	TLS        bool
	CAFile     string
	ServerName string
}

var (
	cliArgs                       = func() []string { return os.Args }
	cliGetenv                     = os.Getenv
	cliStdout           io.Writer = os.Stdout
	cliStderr           io.Writer = os.Stderr
	cliExit                       = os.Exit
	cliUpdateCLI                  = updater.UpdateCLI
	cliCheckUpdate                = updater.CheckForUpdate
	cliRunClient                  = runClient
	cliRunRawClient               = runRawClient
	cliStartInspector             = inspector.StartServer
	cliInspectorHost              = "127.0.0.1"
	cliResolveUDPAddr             = net.ResolveUDPAddr
	cliDialUDP                    = net.DialUDP
	cliControlOptions   controlOptions
	cliHandshakeTimeout = 10 * time.Second
)

func main() {
	if code := run(cliArgs(), cliGetenv, cliStdout, cliStderr, cliUpdateCLI, cliRunClient, cliRunRawClient); code != 0 {
		cliExit(code)
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
			notifyUpdate(stdout, Version)
			return runTCPCommand(args[2:], getenv, stdout, stderr, runRawClientFn)
		case "udp":
			notifyUpdate(stdout, Version)
			return runUDPCommand(args[2:], getenv, stdout, stderr, runRawClientFn)
		default:
			if !strings.HasPrefix(args[1], "-") {
				fmt.Fprintf(stderr, "Error: unknown command %q\n", args[1])
				return 1
			}
		}
	}

	notifyUpdate(stdout, Version)
	return runHTTPCommand(args, getenv, stdout, stderr, runClientFn)
}

func notifyUpdate(w io.Writer, currentVersion string) {
	if latest := cliCheckUpdate(currentVersion); latest != "" {
		fmt.Fprintf(w, "\nA new version of Ratatosk is available (%s). Run \"ratatosk self-update\" to upgrade.\n", latest)
	}
}

func runHTTPCommand(
	args []string,
	getenv func(string) string,
	stdout, stderr io.Writer,
	runClientFn func(string, int, string) error,
) int {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverDefault := getenv("RATATOSK_SERVER")
	if serverDefault == "" {
		serverDefault = "localhost:7000"
	}
	server := flags.String("server", serverDefault, "relay server address (host:port)")
	port := flags.Int("port", 3000, "local port to expose")
	streamer := flags.Bool("streamer", false, "redact sensitive data from output for streaming")
	basicAuth := flags.String("basic-auth", "", "require basic auth for tunnel visitors (format: user:pass)")
	inspectorHost := flags.String("inspector-host", "127.0.0.1", "bind address for the inspector web UI (use 0.0.0.0 for all interfaces)")
	security, err := addControlFlags(flags, getenv)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "Error: unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return 1
	}

	if *port <= 0 || *port > 65535 {
		fmt.Fprintf(stderr, "Error: invalid port %d\n", *port)
		return 1
	}

	if *basicAuth != "" && !strings.Contains(*basicAuth, ":") {
		fmt.Fprintf(stderr, "Error: --basic-auth must be in 'user:pass' format\n")
		return 1
	}

	redact.Enabled = *streamer
	cliInspectorHost = *inspectorHost
	options, err := security.options(*server)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	cliControlOptions = options
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
	serverDefault := getenv("RATATOSK_SERVER")
	if serverDefault == "" {
		serverDefault = "localhost:7000"
	}
	server := flags.String("server", serverDefault, "relay server address (host:port)")
	security, err := addControlFlags(flags, getenv)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if err := flags.Parse(reorderProtoArgs(args)); err != nil {
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

	slog.SetDefault(slog.New(slog.NewTextHandler(stdout, nil)))
	options, err := security.options(*server)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	cliControlOptions = options

	if err := runRawClientFn(*server, port, proto); err != nil {
		slog.Error("client error", "error", err)
		return 1
	}
	return 0
}

func reorderProtoArgs(args []string) []string {
	flagArgs := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--server" || arg == "-server" || arg == "--ca" || arg == "-ca" || arg == "--server-name" || arg == "-server-name" {
			flagArgs = append(flagArgs, arg)
			if i+1 < len(args) {
				flagArgs = append(flagArgs, args[i+1])
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "--server=") || strings.HasPrefix(arg, "-server=") || strings.HasPrefix(arg, "--ca=") || strings.HasPrefix(arg, "-ca=") || strings.HasPrefix(arg, "--server-name=") || strings.HasPrefix(arg, "-server-name=") || strings.HasPrefix(arg, "--tls=") || strings.HasPrefix(arg, "-tls=") || arg == "--tls" || arg == "-tls" {
			flagArgs = append(flagArgs, arg)
			continue
		}
		positional = append(positional, arg)
	}

	return append(flagArgs, positional...)
}

type controlFlagValues struct {
	token      string
	tokenFile  string
	tls        *bool
	caFile     *string
	serverName *string
}

func addControlFlags(flags *flag.FlagSet, getenv func(string) string) (controlFlagValues, error) {
	tlsEnabled := false
	tlsValue := getenv("RATATOSK_CONTROL_TLS")
	if tlsValue == "" {
		tlsValue = getenv("RATATOSK_CONTROL_TLS_ENABLED")
	}
	if tlsValue != "" {
		parsed, err := strconv.ParseBool(tlsValue)
		if err != nil {
			return controlFlagValues{}, fmt.Errorf("control TLS environment value must be a boolean")
		}
		tlsEnabled = parsed
	}
	token := getenv("RATATOSK_CONTROL_TOKEN")
	if token == "" {
		token = getenv("RATATOSK_AUTH_TOKEN")
	}
	tokenFile := getenv("RATATOSK_CONTROL_TOKEN_FILE")
	caFile := getenv("RATATOSK_CONTROL_CA_FILE")
	if caFile == "" {
		caFile = getenv("RATATOSK_CA")
	}
	serverName := getenv("RATATOSK_CONTROL_SERVER_NAME")
	if serverName == "" {
		serverName = getenv("RATATOSK_SERVER_NAME")
	}
	return controlFlagValues{
		token:      token,
		tokenFile:  tokenFile,
		tls:        flags.Bool("tls", tlsEnabled, "verify the relay control-plane TLS certificate"),
		caFile:     flags.String("ca", caFile, "custom CA certificate for control-plane TLS"),
		serverName: flags.String("server-name", serverName, "TLS server name override"),
	}, nil
}

func (values controlFlagValues) options(serverAddr string) (controlOptions, error) {
	tlsEnabled := *values.tls || *values.caFile != "" || *values.serverName != "" || !isLoopbackRelay(serverAddr)
	if !tlsEnabled {
		if values.token != "" || values.tokenFile != "" {
			return controlOptions{}, errors.New("control tokens require TLS")
		}
		return controlOptions{}, nil
	}
	token, err := control.LoadToken(values.token, values.tokenFile)
	if err != nil {
		return controlOptions{}, err
	}
	return controlOptions{Token: token, TLS: true, CAFile: *values.caFile, ServerName: *values.serverName}, nil
}

func isLoopbackRelay(serverAddr string) bool {
	host, _, err := net.SplitHostPort(serverAddr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func buildControlTLSConfig(serverAddr, caFile, serverName string) (*tls.Config, error) {
	if serverName == "" {
		host, _, err := net.SplitHostPort(serverAddr)
		if err != nil {
			return nil, fmt.Errorf("invalid relay server address %q: %w", serverAddr, err)
		}
		serverName = host
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	if caFile == "" {
		return config, nil
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	pemBytes, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("reading custom CA: %w", err)
	}
	if !roots.AppendCertsFromPEM(pemBytes) {
		return nil, errors.New("custom CA file contains no valid certificates")
	}
	config.RootCAs = roots
	return config, nil
}

// connectAndHandshake dials the relay server, creates a yamux session,
// and performs the tunnel handshake. The caller is responsible for
// closing the returned connection and session.
func connectAndHandshake(serverAddr string, tunnelReq *protocol.TunnelRequest) (net.Conn, *yamux.Session, *protocol.TunnelResponse, error) {
	var tlsConfig *tls.Config
	var err error
	if cliControlOptions.TLS {
		tlsConfig, err = buildControlTLSConfig(serverAddr, cliControlOptions.CAFile, cliControlOptions.ServerName)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	conn, err := net.DialTimeout("tcp", serverAddr, cliHandshakeTimeout)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to connect to relay server at %s: %w", serverAddr, err)
	}
	if err := conn.SetDeadline(time.Now().Add(cliHandshakeTimeout)); err != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("setting handshake deadline: %w", err)
	}
	if tlsConfig != nil {
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return nil, nil, nil, fmt.Errorf("control TLS handshake failed: %w", err)
		}
		conn = tlsConn
		if err := control.AuthenticateAsClient(conn, cliControlOptions.Token, cliHandshakeTimeout); err != nil {
			conn.Close()
			return nil, nil, nil, fmt.Errorf("control authentication failed: %w", err)
		}
	}
	slog.Info("connected to relay server", "addr", serverAddr)

	session, err := tunnel.NewClientSession(conn)
	if err != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("failed to create yamux session: %w", err)
	}

	controlStream, err := session.Open()
	if err != nil {
		session.Close()
		conn.Close()
		return nil, nil, nil, fmt.Errorf("failed to open control stream: %w", err)
	}

	if err := protocol.WriteRequest(controlStream, tunnelReq); err != nil {
		controlStream.Close()
		session.Close()
		conn.Close()
		return nil, nil, nil, fmt.Errorf("failed to send tunnel request: %w", err)
	}

	resp, err := protocol.ReadResponse(controlStream)
	if err != nil {
		controlStream.Close()
		session.Close()
		conn.Close()
		return nil, nil, nil, fmt.Errorf("failed to read tunnel response: %w", err)
	}
	controlStream.Close()

	if !resp.Success {
		session.Close()
		conn.Close()
		return nil, nil, nil, fmt.Errorf("tunnel creation failed: %s", resp.Error)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		session.Close()
		conn.Close()
		return nil, nil, nil, fmt.Errorf("clearing handshake deadline: %w", err)
	}

	return conn, session, resp, nil
}

// printHTTPStatus prints the startup banner for an HTTP tunnel.
func printHTTPStatus(tunnelURL string, localPort int, basicAuth string, inspectorAddr string, inspectorErr error) {
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
}

// acceptHTTPStreams accepts yamux streams and proxies each one to the
// local address via the inspector.
func acceptHTTPStreams(session *yamux.Session, localAddr string, logger *inspector.Logger) error {
	for {
		stream, err := session.Accept()
		if err != nil {
			if errors.Is(err, io.EOF) {
				slog.Info("session closed by server")
				return nil
			}
			return fmt.Errorf("failed to accept tunnel stream: %w", err)
		}
		go handleStream(stream, localAddr, logger)
	}
}

func runClient(serverAddr string, localPort int, basicAuth string) error {
	tunnelReq := &protocol.TunnelRequest{Protocol: protocol.ProtoHTTP, LocalPort: localPort, BasicAuth: basicAuth}
	conn, session, resp, err := connectAndHandshake(serverAddr, tunnelReq)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer session.Close()

	logger := inspector.NewLogger()
	inspectorServer, inspectorErr := cliStartInspector(logger, cliInspectorHost)
	if inspectorServer != nil {
		defer inspectorServer.Close()
	}

	tunnelURL := resp.URL
	if tunnelURL == "" {
		tunnelURL = fmt.Sprintf("http://%s.localhost:8080", resp.Subdomain)
	}

	inspectorAddr := ""
	if inspectorServer != nil {
		inspectorAddr = inspectorServer.Addr
	}
	printHTTPStatus(tunnelURL, localPort, basicAuth, inspectorAddr, inspectorErr)

	localAddr := fmt.Sprintf("localhost:%d", localPort)
	return acceptHTTPStreams(session, localAddr, logger)
}

func handleStream(stream net.Conn, localAddr string, logger *inspector.Logger) {
	defer stream.Close()
	inspector.HandleStream(stream, localAddr, logger)
}

func runRawClient(serverAddr string, localPort int, proto string) error {
	localAddr := fmt.Sprintf("localhost:%d", localPort)
	req := &protocol.TunnelRequest{Protocol: proto, LocalPort: localPort}
	conn, session, resp, err := connectAndHandshake(serverAddr, req)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer session.Close()

	fmt.Println()
	fmt.Println("Ratatosk                        (Ctrl+C to quit)")
	fmt.Println()
	fmt.Printf("Forwarding      %s:%d -> %s (%s)\n", serverAddr, resp.Port, localAddr, proto)
	fmt.Println()

	for {
		stream, err := session.Accept()
		if err != nil {
			if errors.Is(err, io.EOF) {
				slog.Info("session closed by server")
				return nil
			}
			return fmt.Errorf("failed to accept tunnel stream: %w", err)
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

	tunnel.ProxyConnections(local, stream)
}

func handleUDPStream(stream net.Conn, localAddr string) {
	defer stream.Close()

	udpAddr, err := cliResolveUDPAddr("udp", localAddr)
	if err != nil {
		slog.Error("failed to resolve UDP address", "addr", localAddr, "error", err)
		return
	}
	local, err := cliDialUDP("udp", nil, udpAddr)
	if err != nil {
		slog.Error("failed to connect to local UDP service", "addr", localAddr, "error", err)
		return
	}
	defer local.Close()

	// stream -> local UDP
	go func() {
		defer local.Close()
		for {
			data, err := tunnel.ReadFrame(stream)
			if err != nil {
				return
			}
			if _, err := local.Write(data); err != nil {
				return
			}
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
