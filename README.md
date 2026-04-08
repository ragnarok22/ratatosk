<p align="center">
  <img src="ratatosk-logo.png" alt="Ratatosk Logo" width="200" />
</p>

<h1 align="center">Ratatosk</h1>

<p align="center">

![Status: Alpha](https://img.shields.io/badge/status-alpha-orange)
[![Go Version](https://img.shields.io/badge/go-1.26.1%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Node.js Version](https://img.shields.io/badge/node-20%2B-339933?logo=node.js&logoColor=white)](https://nodejs.org/)
[![License: GPLv3](https://img.shields.io/badge/license-GPLv3-blue.svg)](./LICENSE)

</p>

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
│   ├── config/            # Configuration manager (viper)
│   ├── inspector/         # Traffic monitoring and logging
│   ├── tunnel/            # TCP multiplexing (yamux)
│   └── protocol/          # Message formats for server-client communication
├── deploy/                # Systemd, Docker, and example config
├── Makefile
├── go.mod
└── go.sum
```

## Prerequisites

- [Go](https://go.dev/) 1.26+
- [Node.js](https://nodejs.org/) 20+ and [pnpm](https://pnpm.io/) (for the dashboard)

## Quick Start (Local Development)

### 1. Start the Relay Server

```sh
make dev-server
```

The server starts with default settings — no config file needed:
- `:7000` TCP control plane (client connections)
- `:8080` public HTTP proxy
- `:8081` admin dashboard

### 2. Connect the CLI Client

In a separate terminal:

```sh
make dev-cli
```

The client connects to `localhost:7000`, establishes a yamux session, and receives a tunnel URL like `http://quick-fox-1234.localhost:8080`.

### 3. Start the Dashboard (development)

```sh
make dev-dashboard
```

Opens the Vite dev server with hot-reload, proxying API calls to the admin server.

## Installation

### From Source

```sh
git clone https://github.com/reinier-hernandez/ratatosk.git
cd ratatosk
make build
```

This builds both binaries into `bin/`:
- `bin/server` — the relay server (with embedded dashboard)
- `bin/cli` — the tunnel client

### Docker

```sh
docker build -f deploy/Dockerfile.server -t ratatosk-server .
docker run -p 7000:7000 -p 8080:8080 -p 8081:8081 ratatosk-server
```

To pass a config file:

```sh
docker run \
  -v /path/to/ratatosk.yaml:/etc/ratatosk/ratatosk.yaml:ro \
  -p 7000:7000 -p 443:443 -p 8081:8081 \
  ratatosk-server
```

## Configuration

The relay server reads configuration from a `ratatosk.yaml` file, environment variables, or built-in defaults. No config file is required — the server runs out of the box with sane defaults.

### Config File

The server searches for `ratatosk.yaml` in these locations (first match wins):

1. `/etc/ratatosk/ratatosk.yaml`
2. `$HOME/.ratatosk/ratatosk.yaml`
3. `./ratatosk.yaml` (current directory)

Copy the example config as a starting point:

```sh
cp deploy/ratatosk.yaml.example ratatosk.yaml
```

### Full Configuration Reference

```yaml
# Base domain for tunnel subdomains.
# Tunnels are accessible at <subdomain>.<base_domain>.
base_domain: localhost

# Port for the public HTTP(S) proxy.
public_port: 8080

# Port for the admin dashboard and API.
admin_port: 8081

# Port for the TCP control plane (CLI client connections).
control_port: 7000

# TLS settings (requires a wildcard certificate for *.base_domain).
tls_enabled: false
tls_cert_file: ""
tls_key_file: ""
```

### Environment Variables

Every config option can be set via environment variables with the `RATATOSK_` prefix. Environment variables override config file values.

| Variable | Default | Description |
|---|---|---|
| `RATATOSK_BASE_DOMAIN` | `localhost` | Base domain for tunnel subdomains |
| `RATATOSK_PUBLIC_PORT` | `8080` | Public HTTP(S) proxy port |
| `RATATOSK_ADMIN_PORT` | `8081` | Admin dashboard port |
| `RATATOSK_CONTROL_PORT` | `7000` | TCP control plane port |
| `RATATOSK_TLS_ENABLED` | `false` | Enable TLS on the public proxy |
| `RATATOSK_TLS_CERT_FILE` | | Path to TLS certificate (PEM) |
| `RATATOSK_TLS_KEY_FILE` | | Path to TLS private key (PEM) |

## Production Deployment

### DNS Setup

Point a wildcard DNS record to your VPS:

```
*.tunnel.example.com  A  <your-vps-ip>
```

### TLS with Let's Encrypt

Obtain a wildcard certificate using a DNS-01 challenge (e.g. with [certbot](https://certbot.eff.org/)):

```sh
certbot certonly --manual --preferred-challenges dns \
  -d "*.tunnel.example.com" -d "tunnel.example.com"
```

Then configure the server:

```yaml
base_domain: tunnel.example.com
public_port: 443
tls_enabled: true
tls_cert_file: /etc/letsencrypt/live/tunnel.example.com/fullchain.pem
tls_key_file: /etc/letsencrypt/live/tunnel.example.com/privkey.pem
```

When TLS is enabled, the server also starts an HTTP listener on port 80 that redirects all traffic to HTTPS.

### Systemd

Install the relay server as a system service:

```sh
# Create a dedicated user
sudo useradd --system --no-create-home --shell /usr/sbin/nologin ratatosk

# Install the binary and config
sudo cp bin/server /usr/local/bin/ratatosk-server
sudo mkdir -p /etc/ratatosk /var/log/ratatosk
sudo cp deploy/ratatosk.yaml.example /etc/ratatosk/ratatosk.yaml
sudo chown -R ratatosk:ratatosk /etc/ratatosk /var/log/ratatosk

# Edit the config
sudo editor /etc/ratatosk/ratatosk.yaml

# Install and start the service
sudo cp deploy/ratatosk.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ratatosk
```

Check status:

```sh
sudo systemctl status ratatosk
sudo journalctl -u ratatosk -f
```

The Systemd unit uses `AmbientCapabilities=CAP_NET_BIND_SERVICE` so the server can bind to ports 80 and 443 without running as root.

## Testing

```sh
make test          # run all tests
make test-race     # run with the race detector
make coverage      # generate and display coverage report
```

## How It Works

1. The CLI client dials the Relay Server over TCP and wraps the connection in a [yamux](https://github.com/hashicorp/yamux) multiplexed session.
2. Multiple logical streams run over this single TCP connection — no extra ports needed on the local router.
3. The Relay Server accepts public HTTP traffic and routes it through the appropriate yamux stream back to the client, which forwards it to your local service.

## Community

Please read the [Code of Conduct](./CODE_OF_CONDUCT.md) before participating in issues, pull requests, or discussions.
Contribution workflow and expectations are documented in [CONTRIBUTING.md](./CONTRIBUTING.md).
For responsible vulnerability disclosure, see the [Security Policy](./SECURITY.md).

## License

Ratatosk is licensed under the GNU General Public License v3.0. See [LICENSE](./LICENSE) for the full text.
