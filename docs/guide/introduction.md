# Introduction

Ratatosk is an open-source, self-hosted reverse proxy and tunneling tool. It lets you expose local web servers to the internet securely, bypassing NAT and local firewalls -- a free alternative to [ngrok](https://ngrok.com).

## Why Ratatosk?

If you've ever needed to share a local development server with a colleague, demo a project to a client, or test webhooks against a service running on `localhost`, you've probably reached for ngrok. Ratatosk gives you the same capability without depending on a third-party service:

- **No usage limits** -- tunnel as much traffic as your VPS can handle.
- **No account required** -- install and run immediately.
- **Full control** -- you own the server, the domain, and the data flowing through it.

## How It Works

Ratatosk has three components:

1. **Relay Server** -- runs on a public VPS, listens for incoming HTTP traffic, and routes it to the correct connected client based on generated subdomains.
2. **CLI Client** -- runs on your local machine. It establishes an outbound, persistent, multiplexed TCP connection to the relay server and forwards tunneled requests to a local port (e.g., `localhost:3000`).
3. **Dashboard** -- a React single-page application embedded directly into the relay server binary. It provides a UI to monitor active tunnels, bandwidth, and real-time traffic via WebSockets.

The core of Ratatosk relies on [yamux](https://github.com/hashicorp/yamux) to multiplex a single TCP control channel into multiple concurrent logical streams. This means the CLI client only needs one outbound TCP connection -- no extra ports need to be opened on your local router.

## What's Next?

- [Install Ratatosk](/guide/installation) on your machine and your server.
- Follow the [Getting Started](/guide/getting-started) guide to create your first tunnel.
- Read about [Deployment](/guide/deployment) to set up a production environment.
