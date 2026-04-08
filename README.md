# Ratatosk

> [!WARNING]
> Ratatosk is currently in alpha. Expect rough edges and frequent breaking changes while the core architecture, APIs, and workflows are still being stabilized.

Open-source, self-hosted reverse proxy and tunneling tool. A free alternative to ngrok.

Expose local web servers to the internet securely, bypassing NAT and local firewalls.

## Architecture

Ratatosk is a Go monorepo with three components:

- **Relay Server** (`cmd/server`) — Runs on a public VPS. Listens for CLI client connections over a multiplexed TCP channel and routes incoming public traffic to the correct tunnel via generated subdomains.
- **CLI Client** (`cmd/cli`) — Runs on your local machine. Establishes a persistent, multiplexed TCP connection to the Relay Server using [yamux](https://github.com/hashicorp/yamux) and forwards tunneled requests to a local port.
- **Dashboard** (`cmd/server/dashboard`) — A React + Vite SPA embedded into the server binary via `go:embed`. Monitors active tunnels, bandwidth, and real-time traffic.

## Project Structure

```
ratatosk/
├── cmd/
│   ├── server/            # Relay server entry point
│   │   └── dashboard/     # React + Vite frontend
│   └── cli/               # CLI client entry point
├── internal/
│   ├── tunnel/            # TCP multiplexing (yamux)
│   └── protocol/          # Message formats for server-client communication
├── Makefile
├── go.mod
└── go.sum
```

## Prerequisites

- [Go](https://go.dev/) 1.26+
- [Node.js](https://nodejs.org/) 20+ and [pnpm](https://pnpm.io/) (for the dashboard)

## Quick Start

### 1. Start the Relay Server

```sh
make dev-server
```

The server listens on `:7000` for client TCP connections.

### 2. Connect the CLI Client

In a separate terminal:

```sh
make dev-cli
```

The client connects to `localhost:7000`, establishes a yamux session, and verifies the connection with a ping/pong exchange.

### 3. Start the Dashboard (development)

```sh
make dev-dashboard
```

## Testing

```sh
make test          # run all tests
make test-race     # run with the race detector
make coverage      # generate and display coverage report
```

## Community

Please read the [Code of Conduct](./CODE_OF_CONDUCT.md) before participating in issues, pull requests, or discussions.

## Build

Compile both binaries into `bin/`:

```sh
make build
```

Clean build artifacts:

```sh
make clean
```

## How It Works

1. The CLI client dials the Relay Server over TCP and wraps the connection in a [yamux](https://github.com/hashicorp/yamux) multiplexed session.
2. Multiple logical streams run over this single TCP connection — no extra ports needed on the local router.
3. The Relay Server accepts public HTTP traffic and routes it through the appropriate yamux stream back to the client, which forwards it to your local service.

## License

MIT
