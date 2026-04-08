<p align="center">
  <img src="ratatosk-logo.png" alt="Ratatosk Logo" width="200" />
</p>

<h1 align="center">Ratatosk</h1>

<p align="center">

![Status: Alpha](https://img.shields.io/badge/status-alpha-orange)
[![CI](https://github.com/ragnarok22/ratatosk/actions/workflows/ci.yml/badge.svg)](https://github.com/ragnarok22/ratatosk/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/ragnarok22/ratatosk/branch/main/graph/badge.svg)](https://codecov.io/gh/ragnarok22/ratatosk)
[![Release](https://github.com/ragnarok22/ratatosk/actions/workflows/release.yml/badge.svg)](https://github.com/ragnarok22/ratatosk/actions/workflows/release.yml)
[![Go Version](https://img.shields.io/badge/go-1.26.1%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Node.js Version](https://img.shields.io/badge/node-20%2B-339933?logo=node.js&logoColor=white)](https://nodejs.org/)
[![GitHub Release](https://img.shields.io/github/v/release/ragnarok22/ratatosk)](https://github.com/ragnarok22/ratatosk/releases/latest)
[![License: GPLv3](https://img.shields.io/badge/license-GPLv3-blue.svg)](./LICENSE)

</p>

> [!WARNING]
> Ratatosk is currently in alpha. Expect rough edges and frequent breaking changes while the core architecture, APIs, and workflows are still being stabilized.

Open-source, self-hosted reverse proxy and tunneling tool. Expose local web servers, TCP services, and UDP endpoints to the internet securely, bypassing NAT and local firewalls.

**[Documentation](https://ragnarok22.github.io/ratatosk/)**

## Features

- **Self-hosted** -- run on your own VPS with no usage limits, no accounts, and no vendor lock-in.
- **HTTP, TCP, and UDP tunnels** -- expose web apps, SSH, databases, game servers, and more.
- **Single binary** -- the relay server ships with an embedded React dashboard; no separate frontend install.
- **Multiplexed connections** -- one outbound TCP connection handles thousands of concurrent requests via [yamux](https://github.com/hashicorp/yamux).
- **Basic Auth** -- protect HTTP tunnels with a username and password.
- **Streamer mode** -- redact IPs, tokens, and file paths from output with `--streamer`.
- **Self-update** -- the CLI can update itself with `ratatosk self-update`.

## Quick Start

### Install the CLI

```sh
# Homebrew (macOS / Linux)
brew tap ragnarok22/tap
brew install ratatosk

# Or download a binary
curl -Lo ratatosk https://github.com/ragnarok22/ratatosk/releases/latest/download/ratatosk-cli-$(uname -s | tr A-Z a-z)-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
chmod +x ratatosk && sudo mv ratatosk /usr/local/bin/
```

### Run the Server

```sh
docker run -d --name ratatosk \
  -p 7000:7000 -p 8080:8080 -p 8081:8081 \
  ghcr.io/ragnarok22/ratatosk-server
```

### Create a Tunnel

```sh
ratatosk --port 3000
```

```
Ratatosk                        (Ctrl+C to quit)

Forwarding      http://golden-bifrost-004721.tunnel.example.com -> http://localhost:3000
Web Interface   http://127.0.0.1:4300
```

TCP and UDP tunnels are also supported:

```sh
ratatosk tcp 22        # SSH
ratatosk udp 25565     # Minecraft
```

See the [Getting Started](https://ragnarok22.github.io/ratatosk/guide/getting-started) guide for more.

## Documentation

Full documentation is available at **[ragnarok22.github.io/ratatosk](https://ragnarok22.github.io/ratatosk/)**:

- [Introduction](https://ragnarok22.github.io/ratatosk/guide/introduction) -- what Ratatosk is and how it works
- [Installation](https://ragnarok22.github.io/ratatosk/guide/installation) -- all install methods (Homebrew, binary, source)
- [Getting Started](https://ragnarok22.github.io/ratatosk/guide/getting-started) -- create your first tunnel
- [Deployment](https://ragnarok22.github.io/ratatosk/guide/deployment) -- DNS, TLS, systemd, and Docker on a VPS
- [Homelab & Smart Home](https://ragnarok22.github.io/ratatosk/guide/homelab) -- Docker Compose, Home Assistant add-on
- [CLI Commands](https://ragnarok22.github.io/ratatosk/reference/cli-commands) -- flags, subcommands, and examples
- [Configuration](https://ragnarok22.github.io/ratatosk/reference/configuration) -- YAML config and environment variables
- [Architecture](https://ragnarok22.github.io/ratatosk/reference/architecture) -- request flow and design decisions

## Community

Please read the [Code of Conduct](./CODE_OF_CONDUCT.md) before participating in issues, pull requests, or discussions.
Contribution workflow and expectations are documented in [CONTRIBUTING.md](./CONTRIBUTING.md).
For responsible vulnerability disclosure, see the [Security Policy](./SECURITY.md).

## License

Ratatosk is licensed under the GNU General Public License v3.0. See [LICENSE](./LICENSE) for the full text.
