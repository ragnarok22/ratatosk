# Architecture

This page describes how Ratatosk's components fit together and how traffic flows through the system.

## Components

Ratatosk is built as three logical components inside a Go monorepo:

### Relay Server

The relay server runs on a public VPS. It has three responsibilities:

- **Control plane** (port 7000) -- accepts persistent TCP connections from CLI clients and manages tunnel registration.
- **Public proxy** (port 8080/443) -- receives incoming HTTP traffic and routes it to the correct tunnel based on the subdomain.
- **Admin API and Dashboard** (port 8081) -- serves the embedded React dashboard and exposes a REST/WebSocket API for monitoring.

### CLI Client

The CLI client runs on the user's local machine or homelab. It:

1. Dials the relay server over TCP on the control port.
2. Wraps the connection in a [yamux](https://github.com/hashicorp/yamux) multiplexed session.
3. Forwards incoming tunneled requests to a local port (e.g., `localhost:3000`).
4. Runs a local traffic inspector on `127.0.0.1:4300`.

### Dashboard

A React + TypeScript single-page application built with Vite. It is embedded directly into the relay server binary using Go's `go:embed` directive -- no separate deployment or install needed. The dashboard provides real-time monitoring of active tunnels, bandwidth, and request/response traffic via WebSockets.

## Request Flow

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
│   ├── tunnel/            # TCP multiplexing (yamux), routing, registry
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
- **In-memory state** -- tunnel registry uses Go maps protected by `sync.Mutex`. No external database for the core tunneling functionality.
