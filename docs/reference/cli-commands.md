# CLI Commands

The Ratatosk CLI runs on your local machine and manages tunnel connections to the relay server.

## Usage

```sh
ratatosk [command] [flags]
```

## Commands

| Command | Description |
|---------|-------------|
| `ratatosk --server host:port` | Connect to a specific relay server (default: `localhost:7000`) |
| `ratatosk --port <port>` | Expose a local service (default: 3000) |
| `ratatosk --basic-auth user:pass` | Require HTTP Basic Auth for tunnel visitors |
| `ratatosk --streamer` | Enable streamer mode (redact sensitive data from output) |
| `ratatosk version` | Print the CLI version |
| `ratatosk self-update` | Check for updates and self-update |

## Flags

### `--server`

The relay server address to connect to.

- **Type:** string
- **Default:** `localhost:7000`
- **Environment variable:** `RATATOSK_SERVER`

```sh
ratatosk --server tunnel.example.com:7000 --port 3000
```

When pointing at a remote relay server (e.g., deployed on a VPS), this flag is required. The environment variable is useful for Docker and Home Assistant deployments where flags may not be convenient.

### `--port`

The local port to expose through the tunnel.

- **Type:** integer
- **Default:** `3000`

```sh
ratatosk --port 8080
```

### `--basic-auth`

Require HTTP Basic Authentication for all visitors to the tunnel. The relay server intercepts requests and demands credentials before any traffic is forwarded to your local service.

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

Forwarding      http://quick-fox-1234.tunnel.example.com -> http://localhost:[REDACTED]
Web Interface   http://[REDACTED]
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
