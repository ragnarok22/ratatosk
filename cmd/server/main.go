package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"

	autocert "ratatosk/internal/certmagic"
	"ratatosk/internal/config"
	"ratatosk/internal/control"
	"ratatosk/internal/protocol"
	"ratatosk/internal/tunnel"
	"ratatosk/internal/updater"
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
	RegisterIfAbsent(subdomain string, session *yamux.Session, basicAuth string, protocol string) bool
	UnregisterIfSession(subdomain string, session *yamux.Session) bool
	GetEntry(subdomain string) (*tunnel.TunnelEntry, bool)
	RegisterPort(port int, entry *tunnel.TunnelEntry)
	UnregisterPort(port int)
	RegisterPortIfAbsent(port int, entry *tunnel.TunnelEntry) bool
	UnregisterPortIfEntry(port int, entry *tunnel.TunnelEntry) bool
	GetPortEntry(port int) (*tunnel.TunnelEntry, bool)
}

// portAllocator abstracts port allocation for TCP/UDP tunnel handlers.
type portAllocator interface {
	Allocate() (int, error)
	Release(port int)
}

var Version = "dev"

const (
	maxControlConnections       = 1024
	maxPendingControlHandshakes = 128
)

type certificateManager interface {
	TLSConfig() *tls.Config
	Serve(http.Handler) error
}

var (
	registry         tunnelRegistry = tunnel.NewRegistry()
	cfg              *config.ServerConfig
	portAlloc        portAllocator
	controlTLSConfig *tls.Config
	controlToken     string
	autoTLSManager   certificateManager

	mainStdout              io.Writer = os.Stdout
	mainExit                          = os.Exit
	mainLoadConfig                    = config.LoadConfig
	mainListen                        = net.Listen
	mainListenAndServe                = listenAndServe
	mainListenAndServeTLS             = listenAndServeTLS
	serverStartControlPlane           = startControlPlane
	serverStartAdminServer            = startAdminServer
	serverStartPublicServer           = startPublicServer
	serverGenerateSubdomain           = protocol.GenerateSubdomain
	serverListenTCP                   = net.Listen
	serverResolveUDPAddr              = net.ResolveUDPAddr
	serverListenUDP                   = net.ListenUDP
	mainNewCertmagicManager           = func(ctx context.Context, cfg autocert.Config) (certificateManager, error) {
		return autocert.NewManager(ctx, cfg)
	}
	serverCheckUpdate      = updater.CheckForUpdate
	serverHandshakeTimeout = 10 * time.Second
	activeHTTPServersMu    sync.Mutex
	activeHTTPServers      = make(map[*http.Server]struct{})
)

func listenAndServe(addr string, handler http.Handler) error {
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	trackHTTPServer(server, true)
	defer trackHTTPServer(server, false)
	return server.ListenAndServe()
}

func listenAndServeTLS(addr, certFile, keyFile string, handler http.Handler) error {
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	trackHTTPServer(server, true)
	defer trackHTTPServer(server, false)
	return server.ListenAndServeTLS(certFile, keyFile)
}

func trackHTTPServer(server *http.Server, add bool) {
	activeHTTPServersMu.Lock()
	defer activeHTTPServersMu.Unlock()
	if add {
		activeHTTPServers[server] = struct{}{}
	} else {
		delete(activeHTTPServers, server)
	}
}

func shutdownHTTPServers() {
	activeHTTPServersMu.Lock()
	servers := make([]*http.Server, 0, len(activeHTTPServers))
	for server := range activeHTTPServers {
		servers = append(servers, server)
	}
	activeHTTPServersMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, server := range servers {
		if err := server.Shutdown(ctx); err != nil {
			slog.Warn("HTTP server shutdown failed", "addr", server.Addr, "error", err)
		}
	}
}

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

	stop := make(chan struct{})
	defer func() {
		close(stop)
		shutdownHTTPServers()
	}()
	signalContext, cancelSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelSignals()

	if err := prepareServerSecurity(signalContext); err != nil {
		slog.Error("failed to initialize control security", "error", err)
		return 1
	}

	portAlloc = tunnel.NewPortAllocator(cfg.PortRangeStart, cfg.PortRangeEnd)

	go func(checkFn func(string) string, ver string) {
		if latest := checkFn(ver); latest != "" {
			slog.Warn("a new version of Ratatosk is available",
				"current", ver,
				"latest", latest,
			)
		}
	}(serverCheckUpdate, Version)

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
	case <-signalContext.Done():
		slog.Info("shutdown signal received")
	}
	return 0
}

func prepareServerSecurity(ctx context.Context) error {
	controlTLSConfig = nil
	controlToken = ""
	autoTLSManager = nil

	if cfg.TLSAuto {
		manager, err := mainNewCertmagicManager(ctx, autocert.Config{
			Email:    cfg.TLSEmail,
			Provider: cfg.TLSProvider,
			APIToken: cfg.TLSAPIToken,
			Domains:  []string{cfg.BaseDomain, "*." + cfg.BaseDomain},
		})
		if err != nil {
			return fmt.Errorf("preparing automatic TLS: %w", err)
		}
		autoTLSManager = manager
	}

	if !cfg.ControlTLSEnabled {
		return nil
	}

	token, err := control.LoadToken(cfg.ControlToken, cfg.ControlTokenFile)
	if err != nil {
		return err
	}
	controlToken = token

	certFile, keyFile := cfg.ControlTLSCertFile, cfg.ControlTLSKeyFile
	if certFile == "" && cfg.TLSEnabled {
		certFile, keyFile = cfg.TLSCertFile, cfg.TLSKeyFile
	}
	if certFile != "" {
		certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return fmt.Errorf("loading control TLS key pair: %w", err)
		}
		controlTLSConfig = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
		return nil
	}
	if autoTLSManager == nil {
		return fmt.Errorf("control TLS certificate source is unavailable")
	}
	controlTLSConfig = autoTLSManager.TLSConfig()
	return nil
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
	slog.Info("control plane listening", "addr", cfg.ControlAddr(), "tls", cfg.ControlTLSEnabled)
	var connections sync.Map
	connectionSlots := make(chan struct{}, maxControlConnections)
	handshakeSlots := make(chan struct{}, maxPendingControlHandshakes)

	go func() {
		<-stop
		ln.Close()
		connections.Range(func(key, _ any) bool {
			_ = key.(net.Conn).Close()
			return true
		})
	}()

	go func() {
		var retryDelay time.Duration
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-stop:
					return
				default:
				}
				if retryDelay == 0 {
					retryDelay = 5 * time.Millisecond
				} else {
					retryDelay *= 2
				}
				if retryDelay > time.Second {
					retryDelay = time.Second
				}
				slog.Error("failed to accept connection", "error", err, "retry_delay", retryDelay)
				timer := time.NewTimer(retryDelay)
				select {
				case <-stop:
					timer.Stop()
					return
				case <-timer.C:
				}
				continue
			}
			retryDelay = 0
			select {
			case <-stop:
				conn.Close()
				return
			default:
			}
			select {
			case connectionSlots <- struct{}{}:
			default:
				slog.Warn("control connection limit reached", "remote", conn.RemoteAddr())
				conn.Close()
				continue
			}
			connections.Store(conn, struct{}{})
			select {
			case <-stop:
				conn.Close()
				connections.Delete(conn)
				<-connectionSlots
				return
			default:
			}
			go func() {
				defer func() { <-connectionSlots }()
				defer connections.Delete(conn)
				select {
				case handshakeSlots <- struct{}{}:
				case <-stop:
					conn.Close()
					return
				default:
					slog.Warn("control handshake limit reached", "remote", conn.RemoteAddr())
					conn.Close()
					return
				}
				securedConn, err := secureControlConnection(conn)
				<-handshakeSlots
				if err != nil {
					slog.Warn("control connection rejected", "remote", conn.RemoteAddr(), "error", err)
					conn.Close()
					return
				}
				handleConnection(securedConn)
			}()
		}
	}()

	return nil
}

func secureControlConnection(conn net.Conn) (net.Conn, error) {
	if !cfg.ControlTLSEnabled {
		return conn, nil
	}
	if controlTLSConfig == nil {
		return nil, errors.New("control TLS is not initialized")
	}
	if err := conn.SetDeadline(time.Now().Add(serverHandshakeTimeout)); err != nil {
		return nil, fmt.Errorf("setting TLS handshake deadline: %w", err)
	}
	tlsConn := tls.Server(conn, controlTLSConfig)
	if err := tlsConn.Handshake(); err != nil {
		return nil, fmt.Errorf("TLS handshake failed: %w", err)
	}
	if err := control.AuthenticateAsServer(tlsConn, controlToken, serverHandshakeTimeout); err != nil {
		return nil, err
	}
	return tlsConn, nil
}

func startAdminServer(
	stop <-chan struct{},
	serve func(addr string, handler http.Handler) error,
) <-chan error {
	errs := make(chan error, 1)

	adminHandler := newAdminHandler(registry)
	slog.Info("admin dashboard listening", "addr", cfg.AdminAddr())

	go func() {
		var err error
		if cfg.AdminTLSEnabled {
			err = mainListenAndServeTLS(cfg.AdminAddr(), cfg.AdminTLSCertFile, cfg.AdminTLSKeyFile, adminHandler)
		} else {
			err = serve(cfg.AdminAddr(), adminHandler)
		}
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
			if autoTLSManager == nil {
				errs <- errors.New("automatic TLS is not initialized")
				return
			}
			err := autoTLSManager.Serve(handler)
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
				slog.Info("HTTP redirect server listening", "addr", ":80")
				if err := serve(":80", httpsRedirectHandler(cfg)); err != nil {
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

func httpsRedirectHandler(serverConfig *config.ServerConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := serverConfig.BaseDomain
		requestHost := r.Host
		if parsedHost, _, err := net.SplitHostPort(requestHost); err == nil {
			requestHost = parsedHost
		}
		if subdomain := extractSubdomain(requestHost, serverConfig.BaseDomain); subdomain != "" {
			host = subdomain + "." + serverConfig.BaseDomain
		}
		if serverConfig.PublicPort != 443 {
			host = net.JoinHostPort(host, fmt.Sprintf("%d", serverConfig.PublicPort))
		}
		target := "https://" + host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
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
func cleanupPort(port int, entry *tunnel.TunnelEntry, proto string) {
	if registry.UnregisterPortIfEntry(port, entry) {
		portAlloc.Release(port)
		slog.Info(proto+" tunnel unregistered", "port", port)
	}
}

func handleConnection(conn net.Conn) {
	if err := conn.SetDeadline(time.Now().Add(serverHandshakeTimeout)); err != nil {
		slog.Warn("failed to set tunnel handshake deadline", "error", err)
	}
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
	if err := conn.SetDeadline(time.Time{}); err != nil {
		slog.Warn("failed to clear tunnel handshake deadline", "remote", remote, "error", err)
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
		slog.Warn("unsupported tunnel protocol", "remote", remote, "protocol", req.Protocol)
		sendErrorAndClose(controlStream, "unsupported protocol")
	}
}

func handleHTTPTunnel(session *yamux.Session, controlStream net.Conn, req *protocol.TunnelRequest, remote string) {
	var subdomain string
	for range 10 {
		candidate := serverGenerateSubdomain()
		if registry.RegisterIfAbsent(candidate, session, req.BasicAuth, protocol.ProtoHTTP) {
			subdomain = candidate
			break
		}
	}
	if subdomain == "" {
		sendErrorAndClose(controlStream, "failed to generate unique subdomain")
		return
	}

	resp := &protocol.TunnelResponse{Subdomain: subdomain, URL: cfg.TunnelURL(subdomain), Success: true}
	if err := protocol.WriteResponse(controlStream, resp); err != nil {
		slog.Error("failed to send tunnel response", "remote", remote, "error", err)
		registry.UnregisterIfSession(subdomain, session)
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

	registry.UnregisterIfSession(subdomain, session)
	slog.Info("tunnel unregistered", "subdomain", subdomain, "remote", remote)
}

func handleTCPTunnel(session *yamux.Session, controlStream net.Conn, req *protocol.TunnelRequest, remote string) {
	port, err := portAlloc.Allocate()
	if err != nil {
		slog.Error("TCP port allocation failed", "remote", remote, "error", err)
		sendErrorAndClose(controlStream, "unable to create tunnel")
		return
	}

	ln, err := serverListenTCP("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		portAlloc.Release(port)
		slog.Error("failed to listen for TCP tunnel", "remote", remote, "port", port, "error", err)
		sendErrorAndClose(controlStream, "unable to create tunnel")
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
	if !registry.RegisterPortIfAbsent(port, entry) {
		ln.Close()
		portAlloc.Release(port)
		slog.Error("allocated TCP port was already registered", "remote", remote, "port", port)
		sendErrorAndClose(controlStream, "unable to create tunnel")
		return
	}

	resp := &protocol.TunnelResponse{Port: port, Success: true}
	if err := protocol.WriteResponse(controlStream, resp); err != nil {
		slog.Error("failed to send tunnel response", "remote", remote, "error", err)
		ln.Close()
		cleanupPort(port, entry, "TCP")
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
	cleanupPort(port, entry, "TCP")
}

func handleUDPTunnel(session *yamux.Session, controlStream net.Conn, req *protocol.TunnelRequest, remote string) {
	port, err := portAlloc.Allocate()
	if err != nil {
		slog.Error("UDP port allocation failed", "remote", remote, "error", err)
		sendErrorAndClose(controlStream, "unable to create tunnel")
		return
	}

	udpAddr, err := serverResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		portAlloc.Release(port)
		slog.Error("failed to resolve UDP tunnel address", "remote", remote, "port", port, "error", err)
		sendErrorAndClose(controlStream, "unable to create tunnel")
		return
	}

	udpConn, err := serverListenUDP("udp", udpAddr)
	if err != nil {
		portAlloc.Release(port)
		slog.Error("failed to listen for UDP tunnel", "remote", remote, "port", port, "error", err)
		sendErrorAndClose(controlStream, "unable to create tunnel")
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
	if !registry.RegisterPortIfAbsent(port, entry) {
		udpConn.Close()
		portAlloc.Release(port)
		slog.Error("allocated UDP port was already registered", "remote", remote, "port", port)
		sendErrorAndClose(controlStream, "unable to create tunnel")
		return
	}

	resp := &protocol.TunnelResponse{Port: port, Success: true}
	if err := protocol.WriteResponse(controlStream, resp); err != nil {
		slog.Error("failed to send tunnel response", "remote", remote, "error", err)
		udpConn.Close()
		cleanupPort(port, entry, "UDP")
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
	cleanupPort(port, entry, "UDP")
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
	if deadline, ok := r.Context().Deadline(); ok {
		if err := stream.SetDeadline(deadline); err != nil {
			slog.Warn("failed to set tunnel stream deadline", "subdomain", subdomain, "error", err)
		}
	}
	cancelWatchDone := make(chan struct{})
	defer close(cancelWatchDone)
	go func() {
		select {
		case <-r.Context().Done():
			stream.Close()
		case <-cancelWatchDone:
		}
	}()

	request := prepareProxyRequest(r, entry.BasicAuth != "")
	if err := request.Write(stream); err != nil {
		slog.Error("failed to write request to stream", "subdomain", subdomain, "error", err)
		http.Error(w, "tunnel error", http.StatusBadGateway)
		return
	}

	slog.Info("proxying request", "subdomain", subdomain, "method", r.Method, "path", r.URL.Path)

	streamReader := bufio.NewReader(stream)
	resp, err := http.ReadResponse(streamReader, request)
	if err != nil {
		slog.Error("failed to read response from stream", "subdomain", subdomain, "error", err)
		http.Error(w, "tunnel error", http.StatusBadGateway)
		return
	}
	if resp.StatusCode == http.StatusSwitchingProtocols {
		proxyUpgrade(w, stream, streamReader, resp, subdomain)
		return
	}
	defer resp.Body.Close()

	removeHopByHopHeaders(resp.Header)
	for key, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		slog.Warn("failed to copy tunnel response", "subdomain", subdomain, "error", err)
	}
}

func prepareProxyRequest(request *http.Request, stripAuthorization bool) *http.Request {
	proxied := request.Clone(request.Context())
	proxied.Header = request.Header.Clone()
	upgrade := proxied.Header.Get("Upgrade")
	removeHopByHopHeaders(proxied.Header)
	if upgrade != "" {
		proxied.Header.Set("Connection", "Upgrade")
		proxied.Header.Set("Upgrade", upgrade)
	}
	if stripAuthorization {
		proxied.Header.Del("Authorization")
	}

	clientIP := request.RemoteAddr
	if host, _, err := net.SplitHostPort(request.RemoteAddr); err == nil {
		clientIP = host
	}
	proto := "http"
	if request.TLS != nil {
		proto = "https"
	}
	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"} {
		proxied.Header.Del(header)
	}
	proxied.Header.Set("Forwarded", fmt.Sprintf("for=%s;proto=%s;host=%q", forwardedFor(clientIP), proto, request.Host))
	proxied.Header.Set("X-Forwarded-For", clientIP)
	proxied.Header.Set("X-Forwarded-Host", request.Host)
	proxied.Header.Set("X-Forwarded-Proto", proto)
	proxied.Header.Set("X-Real-IP", clientIP)
	return proxied
}

func forwardedFor(host string) string {
	if strings.Contains(host, ":") {
		return fmt.Sprintf("%q", "["+host+"]")
	}
	return host
}

func removeHopByHopHeaders(header http.Header) {
	for _, nominated := range strings.Split(header.Get("Connection"), ",") {
		if nominated = strings.TrimSpace(nominated); nominated != "" {
			header.Del(nominated)
		}
	}
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		header.Del(name)
	}
}

func proxyUpgrade(w http.ResponseWriter, stream net.Conn, streamReader *bufio.Reader, response *http.Response, subdomain string) {
	client, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		slog.Error("failed to hijack upgraded connection", "subdomain", subdomain, "error", err)
		http.Error(w, "tunnel error", http.StatusBadGateway)
		return
	}
	defer client.Close()

	upgrade := response.Header.Get("Upgrade")
	removeHopByHopHeaders(response.Header)
	response.Header.Set("Connection", "Upgrade")
	if upgrade != "" {
		response.Header.Set("Upgrade", upgrade)
	}
	if _, err := fmt.Fprintf(buffered, "HTTP/1.1 %s\r\n", response.Status); err != nil {
		return
	}
	if err := response.Header.Write(buffered); err != nil {
		return
	}
	if _, err := buffered.WriteString("\r\n"); err != nil {
		return
	}
	if err := buffered.Flush(); err != nil {
		return
	}

	clientReader := io.Reader(client)
	if buffered.Reader.Buffered() > 0 {
		clientReader = buffered.Reader
	}
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(stream, clientReader)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, streamReader)
		done <- struct{}{}
	}()
	<-done
	stream.Close()
	client.Close()
	<-done
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
	if !ok || !constantTimeEqual(user+":"+pass, expected) {
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
