package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/yamux"

	autocert "ratatosk/internal/certmagic"
	"ratatosk/internal/config"
	"ratatosk/internal/protocol"
	"ratatosk/internal/tunnel"
)

// tunnelLister is the read-only view used by the admin API.
type tunnelLister interface {
	ListTunnels() []tunnel.TunnelInfo
}

// tunnelRegistry manages tunnel lifecycle for HTTP/TCP/UDP handlers.
type tunnelRegistry interface {
	tunnelLister
	Register(subdomain string, session *yamux.Session, basicAuth string, protocol string)
	Unregister(subdomain string)
	HasSubdomain(subdomain string) bool
	GetEntry(subdomain string) (*tunnel.TunnelEntry, bool)
	RegisterPort(port int, entry *tunnel.TunnelEntry)
	UnregisterPort(port int)
	GetPortEntry(port int) (*tunnel.TunnelEntry, bool)
}

// portAllocator abstracts port allocation for TCP/UDP tunnel handlers.
type portAllocator interface {
	Allocate() (int, error)
	Release(port int)
}

var (
	registry  tunnelRegistry = tunnel.NewRegistry()
	cfg       *config.ServerConfig
	portAlloc portAllocator

	mainStdout              io.Writer = os.Stdout
	mainExit                          = os.Exit
	mainLoadConfig                    = config.LoadConfig
	mainListen                        = net.Listen
	mainListenAndServe                = http.ListenAndServe
	mainListenAndServeTLS             = http.ListenAndServeTLS
	serverStartControlPlane           = startControlPlane
	serverStartAdminServer            = startAdminServer
	serverStartPublicServer           = startPublicServer
	serverGenerateSubdomain           = protocol.GenerateSubdomain
	serverListenTCP                   = net.Listen
	serverResolveUDPAddr              = net.ResolveUDPAddr
	serverListenUDP                   = net.ListenUDP
	mainServeCertmagic                = autocert.SetupAndServe
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "init" {
		if code := runInit(); code != 0 {
			mainExit(code)
		}
		return
	}
	if code := runMain(mainStdout, mainLoadConfig, mainListen, mainListenAndServe, mainListenAndServeTLS); code != 0 {
		mainExit(code)
	}
}

func runMain(
	stdout io.Writer,
	loadConfig func() (*config.ServerConfig, error),
	listen func(network, address string) (net.Listener, error),
	serve func(addr string, handler http.Handler) error,
	serveTLS func(addr, certFile, keyFile string, handler http.Handler) error,
) int {
	slog.SetDefault(slog.New(slog.NewTextHandler(stdout, nil)))

	if err := loadServerConfig(loadConfig); err != nil {
		slog.Error("failed to load config", "error", err)
		return 1
	}

	portAlloc = tunnel.NewPortAllocator(cfg.PortRangeStart, cfg.PortRangeEnd)

	stop := make(chan struct{})

	if err := serverStartControlPlane(stop, listen); err != nil {
		slog.Error("failed to start TCP listener", "error", err)
		return 1
	}

	adminErrs := serverStartAdminServer(stop, serve)
	publicErrs := serverStartPublicServer(stop, serve, serveTLS)

	select {
	case err := <-adminErrs:
		if err != nil {
			slog.Error("admin server failed", "error", err)
			return 1
		}
	case err := <-publicErrs:
		if err != nil {
			if cfg.TLSEnabled {
				slog.Error("HTTPS server failed", "error", err)
			} else {
				slog.Error("HTTP server failed", "error", err)
			}
			return 1
		}
	}
	return 0
}

func loadServerConfig(loadConfig func() (*config.ServerConfig, error)) error {
	loaded, err := loadConfig()
	if err != nil {
		return err
	}

	cfg = loaded

	tlsMode := "off"
	if cfg.TLSAuto {
		tlsMode = "auto"
	} else if cfg.TLSEnabled {
		tlsMode = "manual"
	}
	slog.Info("configuration loaded",
		"base_domain", cfg.BaseDomain,
		"public_port", cfg.PublicPort,
		"admin_port", cfg.AdminPort,
		"control_port", cfg.ControlPort,
		"tls_mode", tlsMode,
	)
	return nil
}

func startControlPlane(
	stop <-chan struct{},
	listen func(network, address string) (net.Listener, error),
) error {
	ln, err := listen("tcp", cfg.ControlAddr())
	if err != nil {
		return err
	}
	slog.Info("control plane listening", "addr", cfg.ControlAddr())

	go func() {
		<-stop
		ln.Close()
	}()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-stop:
					return
				default:
				}
				slog.Error("failed to accept connection", "error", err)
				continue
			}
			go handleConnection(conn)
		}
	}()

	return nil
}

func startAdminServer(
	stop <-chan struct{},
	serve func(addr string, handler http.Handler) error,
) <-chan error {
	errs := make(chan error, 1)

	adminHandler := newAdminHandler(registry)
	slog.Info("admin dashboard listening", "addr", cfg.AdminAddr())

	go func() {
		err := serve(cfg.AdminAddr(), adminHandler)
		select {
		case <-stop:
			errs <- nil
		default:
			errs <- err
		}
	}()

	return errs
}

func startPublicServer(
	stop <-chan struct{},
	serve func(addr string, handler http.Handler) error,
	serveTLS func(addr, certFile, keyFile string, handler http.Handler) error,
) <-chan error {
	errs := make(chan error, 1)
	handler := http.HandlerFunc(handleHTTP)

	go func() {
		if cfg.TLSAuto {
			slog.Info("starting automatic TLS via certmagic",
				"base_domain", cfg.BaseDomain,
				"email", cfg.TLSEmail,
				"provider", cfg.TLSProvider,
			)
			cmCfg := autocert.Config{
				Email:    cfg.TLSEmail,
				Provider: cfg.TLSProvider,
				APIToken: cfg.TLSAPIToken,
				Domains:  []string{cfg.BaseDomain, "*." + cfg.BaseDomain},
			}
			err := mainServeCertmagic(cmCfg, handler)
			select {
			case <-stop:
				errs <- nil
			default:
				errs <- err
			}
			return
		}

		if cfg.TLSEnabled {
			// Start HTTP->HTTPS redirect on port 80.
			go func() {
				redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					target := "https://" + r.Host + r.URL.RequestURI()
					http.Redirect(w, r, target, http.StatusMovedPermanently)
				})
				slog.Info("HTTP redirect server listening", "addr", ":80")
				if err := serve(":80", redirect); err != nil {
					select {
					case <-stop:
					default:
						slog.Error("HTTP redirect server failed", "error", err)
					}
				}
			}()

			slog.Info("public HTTPS server listening", "addr", cfg.PublicAddr())
			err := serveTLS(cfg.PublicAddr(), cfg.TLSCertFile, cfg.TLSKeyFile, handler)
			select {
			case <-stop:
				errs <- nil
			default:
				errs <- err
			}
			return
		}

		slog.Info("public HTTP server listening", "addr", cfg.PublicAddr())
		err := serve(cfg.PublicAddr(), handler)
		select {
		case <-stop:
			errs <- nil
		default:
			errs <- err
		}
	}()

	return errs
}

// sendErrorAndClose writes a failure TunnelResponse and closes the stream.
func sendErrorAndClose(stream net.Conn, errMsg string) {
	resp := &protocol.TunnelResponse{Success: false, Error: errMsg}
	protocol.WriteResponse(stream, resp)
	stream.Close()
}

// awaitSessionEnd blocks until the yamux session ends (client disconnects).
// It accepts and immediately closes any stray streams.
func awaitSessionEnd(session *yamux.Session, logKey string, logVal string) {
	for {
		stream, err := session.Accept()
		if err != nil {
			if err == io.EOF {
				slog.Info("client disconnected", logKey, logVal)
			} else {
				slog.Warn("session error", logKey, logVal, "error", err)
			}
			break
		}
		stream.Close()
	}
}

// cleanupPort unregisters a port from the registry and releases it back to the allocator.
func cleanupPort(port int, proto string) {
	registry.UnregisterPort(port)
	portAlloc.Release(port)
	slog.Info(proto+" tunnel unregistered", "port", port)
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

	// Accept the control stream opened by the client for the handshake.
	controlStream, err := session.Accept()
	if err != nil {
		slog.Error("failed to accept control stream", "remote", remote, "error", err)
		return
	}

	req, err := protocol.ReadRequest(controlStream)
	if err != nil {
		slog.Error("failed to read tunnel request", "remote", remote, "error", err)
		controlStream.Close()
		return
	}
	slog.Info("received tunnel request", "remote", remote, "protocol", req.Protocol, "local_port", req.LocalPort)

	switch req.Protocol {
	case protocol.ProtoHTTP:
		handleHTTPTunnel(session, controlStream, req, remote)
	case protocol.ProtoTCP:
		handleTCPTunnel(session, controlStream, req, remote)
	case protocol.ProtoUDP:
		handleUDPTunnel(session, controlStream, req, remote)
	default:
		sendErrorAndClose(controlStream, fmt.Sprintf("unsupported protocol: %s", req.Protocol))
	}
}

func handleHTTPTunnel(session *yamux.Session, controlStream net.Conn, req *protocol.TunnelRequest, remote string) {
	// Generate a human-readable subdomain with collision check.
	var subdomain string
	for range 10 {
		candidate := serverGenerateSubdomain()
		if !registry.HasSubdomain(candidate) {
			subdomain = candidate
			break
		}
	}
	if subdomain == "" {
		sendErrorAndClose(controlStream, "failed to generate unique subdomain")
		return
	}

	registry.Register(subdomain, session, req.BasicAuth, protocol.ProtoHTTP)

	resp := &protocol.TunnelResponse{Subdomain: subdomain, URL: cfg.TunnelURL(subdomain), Success: true}
	if err := protocol.WriteResponse(controlStream, resp); err != nil {
		slog.Error("failed to send tunnel response", "remote", remote, "error", err)
		registry.Unregister(subdomain)
		controlStream.Close()
		return
	}
	controlStream.Close()

	slog.Info("tunnel registered",
		"subdomain", subdomain,
		"url", cfg.TunnelURL(subdomain),
		"remote", remote,
	)

	awaitSessionEnd(session, "subdomain", subdomain)

	registry.Unregister(subdomain)
	slog.Info("tunnel unregistered", "subdomain", subdomain, "remote", remote)
}

func handleTCPTunnel(session *yamux.Session, controlStream net.Conn, req *protocol.TunnelRequest, remote string) {
	port, err := portAlloc.Allocate()
	if err != nil {
		sendErrorAndClose(controlStream, fmt.Sprintf("port allocation failed: %v", err))
		return
	}

	ln, err := serverListenTCP("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		portAlloc.Release(port)
		sendErrorAndClose(controlStream, fmt.Sprintf("failed to listen on port %d: %v", port, err))
		return
	}

	entry := &tunnel.TunnelEntry{
		Session:     session,
		ConnectedAt: timeNow(),
		Protocol:    protocol.ProtoTCP,
		LocalPort:   req.LocalPort,
		PublicPort:  port,
		Listener:    ln,
	}
	registry.RegisterPort(port, entry)

	resp := &protocol.TunnelResponse{Port: port, Success: true}
	if err := protocol.WriteResponse(controlStream, resp); err != nil {
		slog.Error("failed to send tunnel response", "remote", remote, "error", err)
		ln.Close()
		registry.UnregisterPort(port)
		portAlloc.Release(port)
		controlStream.Close()
		return
	}
	controlStream.Close()

	slog.Info("TCP tunnel registered", "port", port, "remote", remote)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tunnel.ServeTCP(ctx, ln, session)

	awaitSessionEnd(session, "port", fmt.Sprintf("%d", port))

	cancel()
	ln.Close()
	cleanupPort(port, "TCP")
}

func handleUDPTunnel(session *yamux.Session, controlStream net.Conn, req *protocol.TunnelRequest, remote string) {
	port, err := portAlloc.Allocate()
	if err != nil {
		sendErrorAndClose(controlStream, fmt.Sprintf("port allocation failed: %v", err))
		return
	}

	udpAddr, err := serverResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		portAlloc.Release(port)
		sendErrorAndClose(controlStream, fmt.Sprintf("failed to resolve UDP address: %v", err))
		return
	}

	udpConn, err := serverListenUDP("udp", udpAddr)
	if err != nil {
		portAlloc.Release(port)
		sendErrorAndClose(controlStream, fmt.Sprintf("failed to listen on UDP port %d: %v", port, err))
		return
	}

	entry := &tunnel.TunnelEntry{
		Session:     session,
		ConnectedAt: timeNow(),
		Protocol:    protocol.ProtoUDP,
		LocalPort:   req.LocalPort,
		PublicPort:  port,
		Listener:    udpConn,
	}
	registry.RegisterPort(port, entry)

	resp := &protocol.TunnelResponse{Port: port, Success: true}
	if err := protocol.WriteResponse(controlStream, resp); err != nil {
		slog.Error("failed to send tunnel response", "remote", remote, "error", err)
		udpConn.Close()
		registry.UnregisterPort(port)
		portAlloc.Release(port)
		controlStream.Close()
		return
	}
	controlStream.Close()

	slog.Info("UDP tunnel registered", "port", port, "remote", remote)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tunnel.ServeUDP(ctx, udpConn, session)

	awaitSessionEnd(session, "port", fmt.Sprintf("%d", port))

	cancel()
	udpConn.Close()
	cleanupPort(port, "UDP")
}

func handleHTTP(w http.ResponseWriter, r *http.Request) {
	subdomain, entry, ok := resolveHTTPTunnel(w, r)
	if !ok {
		return
	}

	stream, err := entry.Session.Open()
	if err != nil {
		slog.Error("failed to open stream", "subdomain", subdomain, "error", err)
		http.Error(w, "tunnel error", http.StatusBadGateway)
		return
	}
	defer stream.Close()

	// Write the HTTP request in wire format into the yamux stream.
	if err := r.Write(stream); err != nil {
		slog.Error("failed to write request to stream", "subdomain", subdomain, "error", err)
		http.Error(w, "tunnel error", http.StatusBadGateway)
		return
	}

	slog.Info("proxying request", "subdomain", subdomain, "method", r.Method, "path", r.URL.Path)

	resp, err := http.ReadResponse(bufio.NewReader(stream), r)
	if err != nil {
		slog.Error("failed to read response from stream", "subdomain", subdomain, "error", err)
		http.Error(w, "tunnel error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers from the tunnel to the client.
	for key, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// resolveHTTPTunnel extracts the subdomain from the request's Host header,
// looks up the tunnel in the registry, and validates basic auth. It writes
// an appropriate HTTP error and returns ok=false if any step fails.
func resolveHTTPTunnel(w http.ResponseWriter, r *http.Request) (subdomain string, entry *tunnel.TunnelEntry, ok bool) {
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	subdomain = extractSubdomain(host, cfg.BaseDomain)
	if subdomain == "" {
		http.Error(w, "invalid host", http.StatusBadRequest)
		return "", nil, false
	}

	entry, found := registry.GetEntry(subdomain)
	if !found {
		http.Error(w, "tunnel not found", http.StatusBadGateway)
		return "", nil, false
	}

	if !checkBasicAuth(w, r, entry.BasicAuth) {
		return "", nil, false
	}

	return subdomain, entry, true
}

// extractSubdomain extracts the subdomain prefix from a host given a base domain.
// For "quick-fox-1234.tunnel.example.com" with base "tunnel.example.com", returns "quick-fox-1234".
// For "quick-fox-1234.localhost" with base "localhost", returns "quick-fox-1234".
// Returns "" if the host doesn't match the expected pattern.
func extractSubdomain(host, baseDomain string) string {
	suffix := "." + baseDomain
	if !strings.HasSuffix(host, suffix) {
		return ""
	}
	sub := strings.TrimSuffix(host, suffix)
	if sub == "" || strings.Contains(sub, ".") {
		return ""
	}
	return sub
}

// checkBasicAuth validates the request's Authorization header against the
// expected "user:pass" credential. Returns true if auth passes (or no auth
// is required). Writes a 401 response and returns false if auth fails.
func checkBasicAuth(w http.ResponseWriter, r *http.Request, expected string) bool {
	if expected == "" {
		return true
	}
	user, pass, ok := r.BasicAuth()
	if !ok || user+":"+pass != expected {
		w.Header().Set("WWW-Authenticate", `Basic realm="Ratatosk Tunnel"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// timeNow is a seam for testing.
var timeNow = time.Now

// initDefaultConfig initializes cfg with defaults for tests that don't call main().
func initDefaultConfig() {
	if cfg == nil {
		cfg = &config.ServerConfig{
			BaseDomain:     "localhost",
			PublicPort:     8080,
			AdminPort:      8081,
			ControlPort:    7000,
			PortRangeStart: 10000,
			PortRangeEnd:   20000,
		}
	}
	if portAlloc == nil {
		portAlloc = tunnel.NewPortAllocator(cfg.PortRangeStart, cfg.PortRangeEnd)
	}
}

func init() {
	initDefaultConfig()
}
