# Getting Started

This guide walks you through creating your first tunnel with Ratatosk.

## Prerequisites

- The [CLI client](/guide/installation#install-the-cli) installed on your local machine.
- A running relay server -- either your own (see [Deployment](/guide/deployment)) or a local dev instance.

## Start a Local Service

First, make sure you have something running locally. For example, a simple HTTP server on port 3000:

```sh
# Node.js
npx serve -l 3000

# Python
python3 -m http.server 3000
```

## Create a Tunnel

Expose your local service to the internet:

```sh
ratatosk --port 3000
```

The CLI connects to the relay server, establishes a [yamux](https://github.com/hashicorp/yamux) session, and prints the public tunnel URL:

```
Ratatosk                        (Ctrl+C to quit)

Forwarding      http://quick-fox-1234.tunnel.example.com -> http://localhost:3000
Web Interface   http://127.0.0.1:4300
```

Anyone on the internet can now access your local service through the forwarding URL. The web interface provides a local traffic inspector for monitoring requests and responses flowing through the tunnel.

## Default Port

If you omit the `--port` flag, Ratatosk defaults to port 3000:

```sh
ratatosk
```

## Local Development

To run both the server and client locally for development:

```sh
# Terminal 1: Start the relay server (builds the dashboard first)
make dev-server

# Terminal 2: Connect the CLI client
make dev-cli

# Terminal 3 (optional): Vite dev server with hot-reload for the dashboard
make dev-dashboard
```

## What's Next?

- Learn about all available [CLI Commands](/reference/cli-commands).
- Configure the server with a [config file](/reference/configuration).
- Deploy to production with TLS and a custom domain following the [Deployment](/guide/deployment) guide.
