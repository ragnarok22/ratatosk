# Architecture

This page describes how Ratatosk's components fit together and how traffic flows through the system.

## Components

Ratatosk is built as three logical components inside a Go monorepo:

### Relay Server

The relay server runs on a public VPS. It has four responsibilities:

- **Control plane** (port 7000) -- accepts persistent TCP connections from CLI clients and manages tunnel registration.
- **Public HTTP proxy** (port 8080/443) -- receives incoming HTTP traffic and routes it to the correct tunnel based on the subdomain.
- **TCP/UDP proxy** (ports 10000-20000) -- allocates dynamic ports for TCP and UDP tunnels and forwards raw traffic to the client via yamux streams.
- **Admin API and Dashboard** (port 8081) -- serves the embedded React dashboard and exposes a REST/WebSocket API for monitoring.

### CLI Client

The CLI client runs on the user's local machine or homelab. It:

1. Dials the relay server over TCP on the control port.
2. Wraps the connection in a [yamux](https://github.com/hashicorp/yamux) multiplexed session.
3. Sends a tunnel request specifying the protocol (HTTP, TCP, or UDP) and local port.
4. For **HTTP tunnels**: forwards incoming HTTP requests to a local port and runs a traffic inspector on `127.0.0.1:4300`.
5. For **TCP tunnels**: accepts yamux streams and bidirectionally copies raw bytes to/from a local TCP service.
6. For **UDP tunnels**: accepts yamux streams, reads length-prefixed frames, and forwards them as UDP datagrams to a local service.

### Dashboard

A React + TypeScript single-page application built with Vite. It is embedded directly into the relay server binary using Go's `go:embed` directive -- no separate deployment or install needed. The dashboard provides real-time monitoring of active tunnels, bandwidth, and request/response traffic via WebSockets.

## Request Flow

### HTTP Tunnels

```
Internet                    VPS                           Local Machine
────────                    ───                           ─────────────
                     ┌──────────────┐
HTTP request ──────► │ Public Proxy │
  abc.tunnel.com     │  (8080/443)  │
                     └──────┬───────┘
                            │ route by subdomain
                     ┌──────▼───────┐
                     │ yamux stream │◄────── single TCP connection ──────► CLI Client
                     │  (port 7000) │                                     (ratatosk)
                     └──────────────┘                                         │
                                                                              ▼
                                                                     localhost:3000
```

1. The CLI client dials the relay server over TCP and wraps the connection in a yamux multiplexed session.
2. Multiple logical streams run over this single TCP connection -- no extra ports needed on the local router.
3. The relay server accepts public HTTP traffic and routes it through the appropriate yamux stream back to the client, which forwards it to your local service.

### TCP/UDP Tunnels

```
Internet                    VPS                           Local Machine
────────                    ───                           ─────────────
                     ┌──────────────┐
TCP/UDP traffic ───► │ Dynamic Port │
  :15432             │ (10000-20000)│
                     └──────┬───────┘
                            │ raw bytes (TCP) or framed datagrams (UDP)
                     ┌──────▼───────┐
                     │ yamux stream │◄────── single TCP connection ──────► CLI Client
                     │  (port 7000) │                                     (ratatosk)
                     └──────────────┘                                         │
                                                                              ▼
                                                                     localhost:22
```

1. The CLI client sends a tunnel request specifying `tcp` or `udp` and the local port.
2. The relay server allocates a random public port from its configurable range (default 10000-20000).
3. For **TCP**: each incoming connection on the public port opens a new yamux stream; bytes are copied bidirectionally.
4. For **UDP**: each unique remote address gets its own yamux stream; datagrams are length-prefixed (4-byte header) to preserve message boundaries.

## Project Structure

```
ratatosk/
├── cmd/
│   ├── server/            # Relay server entry point
│   │   └── dashboard/     # React + Vite frontend (embedded via go:embed)
│   └── cli/               # CLI client entry point
├── internal/
│   ├── config/            # Configuration manager
│   ├── inspector/         # Traffic monitoring and logging
│   ├── tunnel/            # TCP multiplexing (yamux), TCP/UDP proxy, port allocation, registry
│   └── protocol/          # Message formats for server-client communication
├── deploy/                # Systemd, Docker, and example config
├── Makefile
├── go.mod
└── go.sum
```

## Key Design Decisions

- **Go** for the server and CLI -- high concurrency via goroutines, raw TCP socket control, and single static binary output.
- **yamux** for multiplexing -- battle-tested library from HashiCorp that turns one TCP connection into many logical streams.
- **`go:embed`** for the dashboard -- the entire frontend ships inside the server binary. No Node.js runtime needed in production.
- **In-memory state** -- tunnel registry uses Go maps protected by `sync.RWMutex`. No external database for the core tunneling functionality.
- **Port allocation** -- TCP/UDP tunnels use a random-probing allocator within a configurable range, avoiding predictable port assignments.
- **UDP framing** -- UDP datagrams are length-prefixed with a 4-byte big-endian header to preserve message boundaries over the TCP-based yamux connection.
