# Introduction

Ratatosk is an open-source, self-hosted reverse proxy and tunneling tool. It lets you expose local web servers, TCP services, and UDP endpoints to the internet securely, bypassing NAT and local firewalls.

## Why Ratatosk?

If you've ever needed to share a local development server with a colleague, demo a project to a client, or test webhooks against a service running on `localhost`, you know the pain of NAT and firewalls. Ratatosk gives you a tunnel to the public internet without depending on a third-party service:

- **No usage limits** -- tunnel as much traffic as your VPS can handle.
- **No account required** -- install and run immediately.
- **Full control** -- you own the server, the domain, and the data flowing through it.
- **Streamer mode** -- redact IPs, tokens, and file paths from output with `--streamer`, so you can safely record or stream without leaking sensitive data.

## How It Works

Ratatosk has three components:

1. **Relay Server** -- runs on a public VPS, listens for incoming traffic, and routes it to the correct connected client. HTTP tunnels are routed by subdomain; TCP and UDP tunnels use dynamically allocated ports.
2. **CLI Client** -- runs on your local machine. It establishes an outbound, persistent, multiplexed TCP connection to the relay server and forwards tunneled requests to a local port. Supports HTTP (`ratatosk --port 3000`), TCP (`ratatosk tcp 22`), and UDP (`ratatosk udp 25565`) tunnels.
3. **Dashboard** -- a React single-page application embedded directly into the relay server binary. It provides a UI to monitor active tunnels, bandwidth, and real-time traffic via WebSockets.

The core of Ratatosk relies on [yamux](https://github.com/hashicorp/yamux) to multiplex a single TCP control channel into multiple concurrent logical streams. This means the CLI client only needs one outbound TCP connection -- no extra ports need to be opened on your local router. For TCP tunnels, raw bytes are copied bidirectionally over yamux streams. For UDP tunnels, datagrams are length-prefixed to preserve message boundaries.

## What's Next?

- [Install Ratatosk](/guide/installation) on your machine and your server.
- Follow the [Getting Started](/guide/getting-started) guide to create your first tunnel.
- Read about [Deployment](/guide/deployment) to set up a production environment.
