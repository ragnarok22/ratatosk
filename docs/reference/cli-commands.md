# CLI Commands

The Ratatosk CLI runs on your local machine and manages tunnel connections to the relay server.

## Usage

```sh
ratatosk [command] [flags]
```

## Commands

| Command | Description |
|---------|-------------|
| `ratatosk --port <port>` | Expose a local HTTP service (default: 3000) |
| `ratatosk tcp <port>` | Expose a local TCP service (e.g., SSH, PostgreSQL) |
| `ratatosk udp <port>` | Expose a local UDP service (e.g., game servers) |
| `ratatosk --server host:port` | Connect to a specific relay server (default: `localhost:7000`) |
| `ratatosk --tls` | Force verified control-plane TLS for a loopback relay |
| `ratatosk --ca <path>` | Add a custom CA for control-plane TLS |
| `ratatosk --server-name <name>` | Override the control TLS certificate name |
| `ratatosk --basic-auth user:pass` | Require HTTP Basic Auth for tunnel visitors |
| `ratatosk --streamer` | Enable streamer mode (redact sensitive data from output) |
| `ratatosk --inspector-host 0.0.0.0` | Bind the inspector UI to all interfaces (for Docker) |
| `ratatosk version` | Print the CLI version |
| `ratatosk self-update` | Check for updates and self-update |

## Subcommands

### `tcp`

Expose a local TCP service through the tunnel. The relay server allocates a public port and forwards raw TCP traffic bidirectionally.

```sh
ratatosk tcp <port> [--server host:port]
```

Examples:

```sh
ratatosk tcp 22                                    # Expose local SSH
RATATOSK_CONTROL_TOKEN_FILE="$HOME/.config/ratatosk/control-token" \
  ratatosk tcp 5432 --server tunnel.example.com:7000 # Expose PostgreSQL remotely
```

The `--server` flag and `RATATOSK_SERVER` environment variable work the same as with HTTP tunnels.

### `udp`

Expose a local UDP service through the tunnel. UDP datagrams are framed over the yamux TCP connection, preserving message boundaries. Each remote client gets its own multiplexed stream with automatic idle cleanup (60s timeout).

```sh
ratatosk udp <port> [--server host:port]
```

Examples:

```sh
ratatosk udp 25565                                  # Expose Minecraft server
RATATOSK_CONTROL_TOKEN_FILE="$HOME/.config/ratatosk/control-token" \
  ratatosk udp 27015 --server tunnel.example.com:7000 # Expose game server remotely
```

::: tip
TCP and UDP tunnels do not support `--basic-auth`, `--streamer`, or `--inspector-host` flags. These features are specific to HTTP tunnels.
:::

## HTTP Flags

### `--server`

The relay server address to connect to.

- **Type:** string
- **Default:** `localhost:7000`
- **Environment variable:** `RATATOSK_SERVER`

```sh
export RATATOSK_CONTROL_TOKEN_FILE="$HOME/.config/ratatosk/control-token"
ratatosk --server tunnel.example.com:7000 --port 3000
```

When pointing at a remote relay server (e.g., deployed on a VPS), this flag is required. The environment variable is useful for Docker and Home Assistant deployments where flags may not be convenient.

### Control-Plane Security

Remote relay addresses automatically use verified TLS and require exactly one shared-token source. The token must contain at least 32 bytes and is accepted only through `RATATOSK_CONTROL_TOKEN` or `RATATOSK_CONTROL_TOKEN_FILE`; there is no `--token` flag.

```sh
export RATATOSK_CONTROL_TOKEN_FILE="$HOME/.config/ratatosk/control-token"
ratatosk --server tunnel.example.com:7000 --port 3000
```

Use `--ca /path/to/ca.pem` to add a private CA to the system trust pool. Use `--server-name tunnel.example.com` when the dial address differs from the name in the certificate. Certificate-chain and hostname verification are always enforced; Ratatosk has no insecure-skip-verification option.

For a loopback relay, plaintext with no token is the default. `--tls` forces TLS for loopback and then requires a token. `--ca` and `--server-name` also enable TLS. These flags work with HTTP, `tcp`, and `udp` tunnels.

| Environment variable | Purpose |
|---|---|
| `RATATOSK_CONTROL_TOKEN` | Inline shared control token |
| `RATATOSK_CONTROL_TOKEN_FILE` | Path to a shared control-token file |
| `RATATOSK_CONTROL_TLS_ENABLED` | Force control TLS when set to `true`, primarily for loopback |
| `RATATOSK_CONTROL_CA_FILE` | Add a private CA certificate |
| `RATATOSK_CONTROL_SERVER_NAME` | Override the certificate name to verify |

Set exactly one of the two token variables whenever TLS is used. The CLI trims surrounding whitespace from the token or file contents.

### `--port`

The local port to expose through the tunnel.

- **Type:** integer
- **Default:** `3000`

```sh
ratatosk --port 8080
```

### `--basic-auth`

Require HTTP Basic Authentication for all visitors to the tunnel. The relay server intercepts requests and demands credentials before any traffic is forwarded to your local service.

This visitor credential is independent of the shared control token. `--basic-auth` protects access to one HTTP tunnel; it does not authenticate the CLI to the relay and is unrelated to the Cloudflare API token used by the server for DNS-01 challenges.

- **Type:** string
- **Default:** (empty -- no auth, tunnel is public)
- **Format:** `user:pass`

```sh
ratatosk --port 3000 --basic-auth "admin:secret"
```

When enabled, unauthenticated visitors receive a `401 Unauthorized` response with a `WWW-Authenticate: Basic realm="Ratatosk Tunnel"` header. Browsers display their native login dialog automatically.

The credential is sent to the relay server during the tunnel handshake. The server enforces the check before hijacking the connection or opening a yamux stream, so unauthorized requests never consume tunnel bandwidth.

Passwords containing `:` are supported (e.g. `admin:p:ass:word`). Empty passwords are also valid (e.g. `admin:`).

### `--streamer`

Enable streamer mode. When active, sensitive data is replaced with `[REDACTED]` in all CLI output and the traffic inspector.

- **Type:** boolean
- **Default:** `false`

```sh
ratatosk --port 3000 --streamer
```

This is useful when recording videos, streaming on Twitch, or taking screenshots for blog posts. It prevents accidental leaks of:

- **IP addresses** -- IPv4 (e.g. `192.168.1.5:3000`) and IPv6 (e.g. `[::1]:8080`)
- **localhost ports** -- `localhost:3000` becomes `localhost:[REDACTED]`
- **Auth tokens** -- Bearer tokens in log output
- **Sensitive HTTP headers** -- `Authorization`, `Cookie`, `Set-Cookie`, `X-Api-Key`, `X-Auth-Token`, `X-Forwarded-For`, `X-Real-Ip`, and `Proxy-Authorization` values are replaced in the traffic inspector
- **File paths** -- absolute paths starting with `/Users/`, `/home/`, or `/root/`

Example output with streamer mode enabled:

```
Ratatosk                        (Ctrl+C to quit)

Forwarding      http://golden-bifrost-004721.tunnel.example.com -> http://localhost:[REDACTED]
Web Interface   http://[REDACTED]
```

### `--inspector-host`

Set the bind address for the local traffic inspector web UI. By default it binds to `127.0.0.1` (localhost only). Set to `0.0.0.0` to listen on all interfaces, which is required when running inside Docker and connecting from another container (e.g., a Home Assistant integration).

- **Type:** string
- **Default:** `127.0.0.1`

```sh
ratatosk --port 3000 --inspector-host 0.0.0.0
```

## Self-Update

The `self-update` command checks for the latest release and updates the binary in place. If you installed Ratatosk via Homebrew, it defers to `brew upgrade` instead:

```sh
ratatosk self-update
```

## Examples

Expose a React dev server running on port 5173:

```sh
ratatosk --port 5173
```

Expose the default port (3000):

```sh
ratatosk
```

Expose a service with password protection:

```sh
ratatosk --port 8080 --basic-auth "admin:secret"
```

Combine basic auth with streamer mode:

```sh
ratatosk --port 3000 --basic-auth "admin:secret" --streamer
```

Expose a local SSH server:

```sh
ratatosk tcp 22
```

Expose a Minecraft server:

```sh
ratatosk udp 25565
```

Expose a database to a remote colleague:

```sh
export RATATOSK_CONTROL_TOKEN_FILE="$HOME/.config/ratatosk/control-token"
ratatosk tcp 5432 --server tunnel.example.com:7000
```
