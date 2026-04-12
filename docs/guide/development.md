# Development

This page covers everything you need to build, test, and hack on Ratatosk locally.

## Prerequisites

- [Go](https://go.dev/) 1.26+
- [Node.js](https://nodejs.org/) 20+ and [pnpm](https://pnpm.io/) (for the dashboard)

Install dashboard dependencies:

```sh
cd cmd/server/dashboard
pnpm install
```

## Project Structure

```
ratatosk/
├── cmd/
│   ├── server/            # Relay server entry point
│   │   └── dashboard/     # React + Vite frontend (embedded via go:embed)
│   └── cli/               # CLI client entry point
├── internal/
│   ├── config/            # Configuration manager (viper)
│   ├── inspector/         # Traffic monitoring and local inspector UI
│   ├── redact/            # Streamer mode sensitive data redaction
│   ├── tunnel/            # yamux multiplexing, TCP/UDP proxy, port allocation, registry
│   ├── protocol/          # Message formats for server-client communication
│   └── updater/           # CLI self-update logic
├── deploy/                # Systemd unit, Dockerfiles, compose templates, example config
├── docs/                  # VitePress documentation site
├── ratatosk-tunnel/       # Home Assistant add-on
├── Makefile
├── go.mod
└── go.sum
```

Key conventions:

- `cmd/` packages are thin -- they handle initialization and flag parsing only.
- Reusable logic lives in `internal/` so both the server and CLI can share it.
- The dashboard `dist/` folder is embedded into the server binary with `go:embed`.

## Make Targets

Run `make help` to list all targets. Here's the full reference:

### Running locally

| Target | Description |
|--------|-------------|
| `make dev-server` | Build the dashboard and start the relay server |
| `make dev-cli` | Start the CLI client (connects to `localhost:7000`) |
| `make dev-dashboard` | Start the Vite dev server with hot-reload |

### Building

| Target | Description |
|--------|-------------|
| `make build` | Build both server and CLI binaries into `bin/` |
| `make build-dashboard` | Build the React dashboard (runs `pnpm install && pnpm run build`) |
| `make clean` | Remove `bin/`, `coverage.out`, and `dashboard/dist` |

### Testing & quality

| Target | Description |
|--------|-------------|
| `make test` | Run all Go tests |
| `make test-race` | Run tests with the race detector |
| `make coverage` | Generate and display a coverage report |
| `make format` | Format Go source files with `gofmt` |
| `make format-check` | Verify Go files are formatted (CI check) |
| `make lint` | Run `go vet` |

### Dashboard checks

| Target | Description |
|--------|-------------|
| `make dashboard-format-check` | Check dashboard code formatting |
| `make dashboard-lint` | Lint dashboard code |
| `make dashboard-typecheck` | Type-check dashboard TypeScript |

### Documentation

| Target | Description |
|--------|-------------|
| `make docs-dev` | Start the VitePress dev server |
| `make docs-build` | Build the documentation site |
| `make docs-preview` | Preview the built documentation |

## Typical Workflow

Open three terminals:

```sh
# Terminal 1 -- relay server
make dev-server

# Terminal 2 -- CLI client
make dev-cli

# Terminal 3 -- dashboard with hot-reload (optional)
make dev-dashboard
```

The server builds the dashboard before starting. If you're only working on the backend, terminals 1 and 2 are enough.

## Testing

Tests live alongside the code they cover using Go's standard `_test.go` convention:

| File | Covers |
|------|--------|
| `internal/tunnel/registry_test.go` | Registry CRUD, concurrent access |
| `internal/tunnel/multiplexer_test.go` | yamux config, session creation, bidirectional streaming |
| `internal/tunnel/tcpproxy_test.go` | TCP proxy data flow via yamux |
| `internal/tunnel/udpproxy_test.go` | UDP proxy datagram round-trip, multi-client |
| `internal/tunnel/portalloc_test.go` | Port allocator allocation, exhaustion, concurrency |
| `internal/tunnel/udpframe_test.go` | UDP frame encoding/decoding |
| `internal/protocol/message_test.go` | Protocol message serialization, subdomain generation |
| `cmd/server/main_test.go` | HTTP/TCP/UDP tunnel handshakes, handler edge cases |
| `cmd/cli/main_test.go` | Subcommand parsing, HTTP/raw client tests |

Tips:

- Use `net.Pipe()` for in-memory connections in yamux session tests.
- Run `make test-race` before submitting changes that touch shared state or goroutines.
- New packages under `internal/` or `cmd/` should include at least basic `_test.go` coverage.

## Cross-Compiling

Build the server for Linux from any platform:

```sh
make build-dashboard
GOOS=linux GOARCH=amd64 go build -o ratatosk-server ./cmd/server
```

See the [Deployment Guide](./deployment.md) for uploading to a VPS.

## Contributing

See [CONTRIBUTING.md](https://github.com/ragnarok22/ratatosk/blob/main/CONTRIBUTING.md) for coding guidelines, pull request expectations, and testing requirements.
