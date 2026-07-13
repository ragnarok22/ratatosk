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

With the default local relay address, the CLI connects over loopback, establishes a [yamux](https://github.com/hashicorp/yamux) session, and prints the public tunnel URL:

```
Ratatosk                        (Ctrl+C to quit)

Forwarding      http://golden-bifrost-004721.tunnel.example.com -> http://localhost:3000
Web Interface   http://127.0.0.1:4300
```

Anyone on the internet can now access your local service through the forwarding URL. The web interface provides a local traffic inspector for monitoring requests and responses flowing through the tunnel.

## Connect to a Remote Relay

A remote relay requires a shared control token of at least 32 bytes. The CLI accepts it only from an environment variable or file, never a command-line flag. A file avoids placing the secret directly in the process environment:

```sh
export RATATOSK_CONTROL_TOKEN_FILE="$HOME/.config/ratatosk/control-token"
ratatosk --server tunnel.example.com:7000 --port 3000
```

Remote server addresses automatically use TLS with certificate and hostname verification. Do not add `--tls` for a normal remote connection; that flag exists to force TLS when connecting to loopback. If the relay uses a private CA, add `--ca /path/to/ca.pem`; if the address does not match the certificate name, add `--server-name tunnel.example.com`. Verification cannot be disabled.

## Default Port

If you omit the `--port` flag, Ratatosk defaults to port 3000:

```sh
ratatosk
```

## TCP Tunnels

Expose local TCP services like SSH, databases, or any other TCP-based protocol:

```sh
ratatosk tcp 22
```

The relay server allocates a public port and forwards raw TCP traffic:

```
Ratatosk                        (Ctrl+C to quit)

Forwarding      relay.example.com:15432 -> localhost:22 (tcp)
```

Common use cases:

```sh
ratatosk tcp 22            # SSH
ratatosk tcp 5432          # PostgreSQL
ratatosk tcp 3306          # MySQL
ratatosk tcp 6379          # Redis
```

Use `--server` to point at a remote relay:

```sh
export RATATOSK_CONTROL_TOKEN_FILE="$HOME/.config/ratatosk/control-token"
ratatosk tcp 22 --server tunnel.example.com:7000
```

## UDP Tunnels

Expose local UDP services like game servers:

```sh
ratatosk udp 25565
```

```
Ratatosk                        (Ctrl+C to quit)

Forwarding      relay.example.com:18200 -> localhost:25565 (udp)
```

UDP datagrams are framed over the yamux TCP connection, preserving message boundaries. Each remote client gets its own multiplexed stream with automatic idle cleanup (60s timeout).

## Protect with Basic Auth

If you don't want your tunnel to be publicly accessible, add `--basic-auth` to require a username and password from HTTP visitors:

```sh
ratatosk --port 3000 --basic-auth "admin:secret"
```

Visitors will see a browser login dialog before any traffic reaches your local service. See [CLI Commands > --basic-auth](/reference/cli-commands#basic-auth) for details.

Visitor Basic Auth does not replace the control token: Basic Auth controls who can visit the public HTTP tunnel, while the control token authenticates the CLI to the relay. The Cloudflare API token mentioned in the deployment guide is a third credential used only by the server to automate DNS-01 certificates.

## Streamer Mode

If you are streaming, recording, or taking screenshots, add `--streamer` to redact sensitive data (IPs, tokens, file paths) from the terminal output and the traffic inspector:

```sh
ratatosk --port 3000 --streamer
```

See [CLI Commands > --streamer](/reference/cli-commands#streamer) for the full list of redacted patterns.

## What's Next?

- Learn about all available [CLI Commands](/reference/cli-commands).
- Configure the server with a [config file](/reference/configuration).
- Deploy to production with TLS and a custom domain following the [Deployment](/guide/deployment) guide.
- Set up a local dev environment with the [Development](/guide/development) guide.
