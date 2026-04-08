<p align="center">
  <img src="ratatosk-logo.png" alt="Ratatosk Logo" width="200" />
</p>

<h1 align="center">Ratatosk</h1>

<p align="center">

![Status: Alpha](https://img.shields.io/badge/status-alpha-orange)
[![CI](https://github.com/ragnarok22/ratatosk/actions/workflows/ci.yml/badge.svg)](https://github.com/ragnarok22/ratatosk/actions/workflows/ci.yml)
[![Release](https://github.com/ragnarok22/ratatosk/actions/workflows/release.yml/badge.svg)](https://github.com/ragnarok22/ratatosk/actions/workflows/release.yml)
[![Go Version](https://img.shields.io/badge/go-1.26.1%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Node.js Version](https://img.shields.io/badge/node-20%2B-339933?logo=node.js&logoColor=white)](https://nodejs.org/)
[![License: GPLv3](https://img.shields.io/badge/license-GPLv3-blue.svg)](./LICENSE)

</p>

> [!WARNING]
> Ratatosk is currently in alpha. Expect rough edges and frequent breaking changes while the core architecture, APIs, and workflows are still being stabilized.

Open-source, self-hosted reverse proxy and tunneling tool. A free alternative to ngrok. Expose local web servers to the internet securely, bypassing NAT and local firewalls.

## Install the Server

The relay server runs on a public VPS. It accepts CLI client connections and routes public HTTP traffic to the correct tunnel.

### Docker (recommended)

```sh
docker run -d --name ratatosk \
  -p 7000:7000 -p 8080:8080 -p 8081:8081 \
  ghcr.io/ragnarok22/ratatosk-server
```

To pass a config file:

```sh
docker run -d --name ratatosk \
  -v /path/to/ratatosk.yaml:/etc/ratatosk/ratatosk.yaml:ro \
  -p 7000:7000 -p 443:443 -p 8081:8081 \
  ghcr.io/ragnarok22/ratatosk-server
```

### Build from Source

```sh
git clone https://github.com/ragnarok22/ratatosk.git
cd ratatosk
make build
sudo cp bin/server /usr/local/bin/ratatosk-server
```

## Configure the Server

The server runs out of the box with sane defaults — no config file required.

| Port | Purpose |
|------|---------|
| `7000` | TCP control plane (CLI client connections) |
| `8080` | Public HTTP(S) proxy |
| `8081` | Admin dashboard and API |

### Config File

The server searches for `ratatosk.yaml` in these locations (first match wins):

1. `/etc/ratatosk/ratatosk.yaml`
2. `$HOME/.ratatosk/ratatosk.yaml`
3. `./ratatosk.yaml` (current directory)

Copy the example as a starting point:

```sh
cp deploy/ratatosk.yaml.example /etc/ratatosk/ratatosk.yaml
```

```yaml
base_domain: localhost       # Tunnels are <subdomain>.<base_domain>
public_port: 8080            # Public HTTP(S) proxy port
admin_port: 8081             # Admin dashboard port
control_port: 7000           # TCP control plane port
tls_enabled: false           # Enable TLS on the public proxy
tls_cert_file: ""            # Path to TLS certificate (PEM)
tls_key_file: ""             # Path to TLS private key (PEM)
```

### Environment Variables

Every option can be set via environment variables with the `RATATOSK_` prefix. Environment variables override config file values.

| Variable | Default | Description |
|---|---|---|
| `RATATOSK_BASE_DOMAIN` | `localhost` | Base domain for tunnel subdomains |
| `RATATOSK_PUBLIC_PORT` | `8080` | Public HTTP(S) proxy port |
| `RATATOSK_ADMIN_PORT` | `8081` | Admin dashboard port |
| `RATATOSK_CONTROL_PORT` | `7000` | TCP control plane port |
| `RATATOSK_TLS_ENABLED` | `false` | Enable TLS on the public proxy |
| `RATATOSK_TLS_CERT_FILE` | | Path to TLS certificate (PEM) |
| `RATATOSK_TLS_KEY_FILE` | | Path to TLS private key (PEM) |

## Install the CLI

The CLI client runs on your local machine. It opens a persistent, multiplexed connection to the relay server and forwards tunneled requests to a local port.

### Homebrew (macOS / Linux)

```sh
brew tap ragnarok22/tap
brew install ratatosk
```

### Download a Binary

Grab the latest release for your platform from the [Releases](https://github.com/ragnarok22/ratatosk/releases) page, or use the commands below:

```sh
# macOS (Apple Silicon)
curl -Lo ratatosk https://github.com/ragnarok22/ratatosk/releases/latest/download/ratatosk-cli-darwin-arm64
chmod +x ratatosk && sudo mv ratatosk /usr/local/bin/

# macOS (Intel)
curl -Lo ratatosk https://github.com/ragnarok22/ratatosk/releases/latest/download/ratatosk-cli-darwin-amd64
chmod +x ratatosk && sudo mv ratatosk /usr/local/bin/

# Linux (amd64)
curl -Lo ratatosk https://github.com/ragnarok22/ratatosk/releases/latest/download/ratatosk-cli-linux-amd64
chmod +x ratatosk && sudo mv ratatosk /usr/local/bin/
```

On Windows, download `ratatosk-cli-windows-amd64.exe` from the Releases page.

### Build from Source

```sh
git clone https://github.com/ragnarok22/ratatosk.git
cd ratatosk
make build
sudo cp bin/cli /usr/local/bin/ratatosk
```

### Commands

| Command | Description |
|---------|-------------|
| `ratatosk --port <port>` | Expose a local service (default: 3000) |
| `ratatosk version` | Print the CLI version |
| `ratatosk self-update` | Check for updates and self-update (defers to `brew upgrade` if installed via Homebrew) |

### Usage

Expose a local service running on port 3000:

```sh
ratatosk --port 3000
```

The CLI connects to the relay server, establishes a [yamux](https://github.com/hashicorp/yamux) session, and prints the public tunnel URL:

```
Ratatosk                        (Ctrl+C to quit)

Forwarding      http://quick-fox-1234.tunnel.example.com -> http://localhost:3000
Web Interface   http://127.0.0.1:4300
```

The web interface provides a local traffic inspector for monitoring requests and responses flowing through the tunnel.

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
sudo useradd --system --no-create-home --shell /usr/sbin/nologin ratatosk

sudo cp bin/server /usr/local/bin/ratatosk-server
sudo mkdir -p /etc/ratatosk /var/log/ratatosk
sudo cp deploy/ratatosk.yaml.example /etc/ratatosk/ratatosk.yaml
sudo chown -R ratatosk:ratatosk /etc/ratatosk /var/log/ratatosk

sudo editor /etc/ratatosk/ratatosk.yaml

sudo cp deploy/ratatosk.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ratatosk
```

Check status:

```sh
sudo systemctl status ratatosk
sudo journalctl -u ratatosk -f
```

The systemd unit uses `AmbientCapabilities=CAP_NET_BIND_SERVICE` so the server can bind to ports 80 and 443 without running as root.

## Development

### Prerequisites

- [Go](https://go.dev/) 1.26+
- [Node.js](https://nodejs.org/) 20+ and [pnpm](https://pnpm.io/) (for the dashboard)

### Quick Start

```sh
make dev-server      # Start the relay server (builds dashboard first)
make dev-cli         # Connect the CLI client (separate terminal)
make dev-dashboard   # Vite dev server with hot-reload (separate terminal)
```

### Testing

```sh
make test          # run all tests
make test-race     # run with the race detector
make coverage      # generate and display coverage report
```

### Project Structure

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
