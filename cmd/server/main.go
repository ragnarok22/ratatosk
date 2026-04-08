package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/yamux"

	"ratatosk/internal/config"
	"ratatosk/internal/protocol"
	"ratatosk/internal/tunnel"
)

var (
	registry  = tunnel.NewRegistry()
	cfg       *config.ServerConfig
	portAlloc *tunnel.PortAllocator

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
)

func main() {
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
	slog.Info("configuration loaded",
		"base_domain", cfg.BaseDomain,
		"public_port", cfg.PublicPort,
		"admin_port", cfg.AdminPort,
		"control_port", cfg.ControlPort,
		"tls", cfg.TLSEnabled,
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

	registry.Register(subdomain, session, req.BasicAuth)

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
	// Extract subdomain from Host header (e.g. "quick-fox-1234.tunnel.example.com:8080").
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Strip the base domain suffix to extract the subdomain.
	subdomain := extractSubdomain(host, cfg.BaseDomain)
	if subdomain == "" {
		http.Error(w, "invalid host", http.StatusBadRequest)
		return
	}

	entry, ok := registry.GetEntry(subdomain)
	if !ok {
		http.Error(w, "tunnel not found", http.StatusBadGateway)
		return
	}

	if !checkBasicAuth(w, r, entry.BasicAuth) {
		return
	}

	// Hijack the public-facing HTTP connection to get the raw net.Conn.
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		slog.Error("hijacking not supported")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		slog.Error("hijack failed", "error", err)
		return
	}
	defer clientConn.Close()

	// Unwrap TLS connection for raw writes if needed.
	rawConn := clientConn
	if tlsConn, ok := clientConn.(*tls.Conn); ok {
		rawConn = tlsConn
	}

	// Proxy the first request (already parsed by net/http) and then loop
	// to handle subsequent keep-alive requests on the same connection.
	if !proxyRequest(r, subdomain, entry.Session, rawConn) {
		return
	}

	// Read further requests from the hijacked connection (keep-alive).
	reader := clientBuf.Reader
	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			return // client closed connection or malformed request
		}
		// Re-extract subdomain — the Host header could theoretically differ,
		// but in practice it stays the same on a keep-alive connection.
		h := req.Host
		if idx := strings.LastIndex(h, ":"); idx != -1 {
			h = h[:idx]
		}
		sub := extractSubdomain(h, cfg.BaseDomain)
		if sub == "" {
			return
		}
		ent, ok := registry.GetEntry(sub)
		if !ok {
			return
		}
		if ent.BasicAuth != "" {
			user, pass, ok := req.BasicAuth()
			if !ok || user+":"+pass != ent.BasicAuth {
				writeRaw401(rawConn)
				return
			}
		}
		if !proxyRequest(req, sub, ent.Session, rawConn) {
			return
		}
	}
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

// proxyRequest opens a yamux stream, forwards the HTTP request through it,
// and copies the response back to clientConn. Returns true if the connection
// can be reused for another request (keep-alive).
func proxyRequest(r *http.Request, subdomain string, session *yamux.Session, clientConn net.Conn) bool {
	stream, err := session.Open()
	if err != nil {
		slog.Error("failed to open stream", "subdomain", subdomain, "error", err)
		return false
	}
	defer stream.Close()

	// Write the HTTP request in wire format into the yamux stream.
	if err := r.Write(stream); err != nil {
		slog.Error("failed to write request to stream", "subdomain", subdomain, "error", err)
		return false
	}

	slog.Info("proxying request", "subdomain", subdomain, "method", r.Method, "path", r.URL.Path)

	// For requests with a body (POST, PUT, etc.), copy remaining data
	// from the browser to the stream in the background.
	if r.ContentLength > 0 || r.TransferEncoding != nil {
		go io.Copy(stream, clientConn)
	}

	// Copy the response from the yamux stream back to the browser.
	// When the CLI finishes writing the response and closes the stream,
	// this returns with EOF.
	resp, err := http.ReadResponse(bufio.NewReader(stream), r)
	if err != nil {
		slog.Error("failed to read response from stream", "subdomain", subdomain, "error", err)
		return false
	}
	defer resp.Body.Close()

	// Write the response back to the browser in wire format.
	err = resp.Write(clientConn)
	return err == nil
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

// writeRaw401 writes a raw HTTP/1.1 401 response with WWW-Authenticate header
// to a hijacked connection.
func writeRaw401(conn net.Conn) {
	body := "Unauthorized"
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header: http.Header{
			"WWW-Authenticate": {`Basic realm="Ratatosk Tunnel"`},
			"Content-Type":     {"text/plain"},
		},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
	resp.Write(conn)
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
