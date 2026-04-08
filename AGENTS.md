## Repository Directives

- Run coverage and tests with `make` or `pnpm` when possible.
- If creating a commit or PR, do not add co-authors.
- When a bug is reported, start by writing a test that reproduces it. Then use subagents to try fixes and prove the result with a passing test.
- Do not suppress lint warnings just to get clean output.

# Ratatosk: Project Context & Agent Guidelines

## 🎯 Project Overview
**Ratatosk** is an open-source, self-hosted reverse proxy and tunneling tool. It allows users to expose local web servers, TCP services, and UDP endpoints to the internet securely, bypassing NAT and local firewalls. 

The project is built as a **Go Monorepo** containing three main logical components:
1. **The Relay Server (`frps` equivalent):** Runs on a public VPS, listens for incoming public traffic, and routes it to the correct connected local client. HTTP tunnels are routed by subdomain; TCP/UDP tunnels use dynamically allocated ports from a configurable range.
2. **The Dashboard:** A React + Vite Single Page Application (SPA) embedded directly into the Relay Server's Go binary using `go:embed`. It provides a UI to monitor active tunnels, bandwidth, and real-time traffic (via WebSockets).
3. **The CLI Client (`frpc` equivalent):** Runs on the user's local machine/homelab. It establishes an outbound, persistent, multiplexed TCP connection to the Relay Server and forwards incoming tunneled requests to a local port (e.g., `localhost:3000`). Supports HTTP (`ratatosk --port 3000`), TCP (`ratatosk tcp 22`), and UDP (`ratatosk udp 25565`) tunnels.

## 📁 Folder Structure
We are using a standard Go project layout:

```text
ratatosk/
├── go.mod                 # Go module definition
├── cmd/
│   ├── server/            # [PACKAGE 1] The Public Relay Server
│   │   ├── main.go        # Entry point for the VPS server
│   │   └── dashboard/     # [PACKAGE 2] The Frontend
│   │       ├── package.json
│   │       ├── src/       # React + TypeScript source code
│   │       └── vite.config.ts
│   └── cli/               # [PACKAGE 3] The Local Client
│       └── main.go        # Entry point for the CLI tool
├── internal/              # Shared business logic
│   ├── tunnel/            # TCP multiplexing (yamux), TCP/UDP proxy, port allocation, registry
│   └── protocol/          # Data structures and message formats for Server-CLI communication
└── pkg/                   # Publicly exportable libraries (if any)
```

Tech Stack & Key Decisions
Backend & CLI: Go (Golang). Chosen for high concurrency (goroutines for handling thousands of connections), raw TCP socket manipulation, and compiling to a single static binary.

Frontend: React, TypeScript, and Vite.

Integration: The Vite dist/ folder is packaged into the Go server using the go:embed directive.

State Management (V1): In-memory data structures (maps and structs) protected by sync.Mutex. No external database for Phase 1.

Multiplexing: The core magic relies on taking a single TCP control channel and multiplexing it to handle multiple concurrent requests without opening new ports on the local router. HTTP tunnels are routed by subdomain. TCP and UDP tunnels use dynamically allocated server ports with bidirectional proxying (raw bytes for TCP, length-prefixed frames for UDP) over yamux streams.

## Testing

Tests live alongside the code they cover using Go's standard `_test.go` convention:

- `internal/tunnel/registry_test.go` — Registry CRUD and concurrent access (subdomain + port-based).
- `internal/tunnel/multiplexer_test.go` — yamux config, session creation, end-to-end bidirectional streaming.
- `internal/tunnel/tcpproxy_test.go` — TCP proxy bidirectional data flow via yamux.
- `internal/tunnel/udpproxy_test.go` — UDP proxy datagram round-trip and multi-client tests.
- `internal/tunnel/portalloc_test.go` — Port allocator allocation, exhaustion, and concurrency.
- `internal/tunnel/udpframe_test.go` — UDP frame encoding/decoding round-trips and edge cases.
- `cmd/server/main_test.go` — HTTP/TCP/UDP tunnel handshakes and handler edge cases.
- `cmd/cli/main_test.go` — Subcommand parsing (tcp/udp), HTTP client, and raw client tests.

Run tests with Make:

```sh
make test          # go test ./...
make test-race     # go test -race ./...
make coverage      # generate coverage report
```

When adding new packages under `internal/` or `cmd/`, always include a `*_test.go` file with at least basic unit tests. Use `net.Pipe()` to create in-memory connections for yamux session tests. Run `make test-race` before submitting changes to catch concurrency issues.

## Directives
Concurrency Safety: Since Go handles connections via goroutines, always check for race conditions. Use channels or sync.Mutex meticulously when accessing the shared tunnel state manager.

Modularity: Keep the cmd/ packages extremely thin. They should only handle initialization and flag parsing. Push all core logic into the internal/ directories so both the CLI and Server can share protocol definitions.

Step-by-Step Implementation: When asked to implement a feature, do not rewrite the entire file. Provide only the necessary additions or explicitly state where the new blocks of code should be inserted.
