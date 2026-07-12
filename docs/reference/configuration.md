# Configuration

The Ratatosk relay server runs out of the box with sane defaults -- no config file required. For production deployments, you can customize behavior through a YAML config file or environment variables.

## Config File

The server searches for `ratatosk.yaml` in these locations (first match wins):

1. `/etc/ratatosk/ratatosk.yaml`
2. `$HOME/.ratatosk/ratatosk.yaml`
3. `./ratatosk.yaml` (current directory)

Copy the example as a starting point:

```sh
cp deploy/ratatosk.yaml.example /etc/ratatosk/ratatosk.yaml
```

### Options

```yaml
base_domain: localhost       # Tunnels are <subdomain>.<base_domain>
public_port: 8080            # Public HTTP(S) proxy port
admin_host: 127.0.0.1        # Safe local-only dashboard bind
admin_port: 8081             # Admin dashboard port
admin_username: ""           # Admin dashboard Basic Auth username
admin_password: ""
admin_tls_enabled: false     # Required for remote admin binds
admin_tls_cert_file: ""     # Admin server certificate (PEM)
admin_tls_key_file: ""      # Admin private key (PEM)
control_host: 127.0.0.1      # Safe local-only control-plane bind
control_port: 7000           # TCP control plane port
control_token: ""            # Inline shared token; mutually exclusive with token file
control_token_file: ""       # Shared-token file; preferred for deployed secrets
control_tls_enabled: false   # Required unless control_host is a literal loopback IP
control_tls_cert_file: ""    # Optional dedicated control certificate (PEM)
control_tls_key_file: ""     # Optional dedicated control private key (PEM)
tls_enabled: false           # Enable manual TLS on the public proxy
tls_cert_file: ""            # Path to TLS certificate (PEM)
tls_key_file: ""             # Path to TLS private key (PEM)
tls_auto: false              # Enable automatic TLS via Let's Encrypt DNS-01
tls_email: ""                # Email for Let's Encrypt registration
tls_provider: ""             # DNS provider (e.g., "cloudflare")
tls_api_token: ""            # API token for the DNS provider
port_range_start: 10000      # Start of dynamic port range for TCP/UDP tunnels
port_range_end: 20000        # End of dynamic port range (exclusive)
```

## Environment Variables

Every option can be set via environment variables with the `RATATOSK_` prefix. Environment variables override the corresponding config file values. This precedence is per field: configuring `control_token` in YAML and `RATATOSK_CONTROL_TOKEN_FILE` in the environment leaves both final fields set and is rejected. Configure exactly one token source across all layers.

| Variable | Default | Description |
|---|---|---|
| `RATATOSK_BASE_DOMAIN` | `localhost` | Base domain for tunnel subdomains |
| `RATATOSK_PUBLIC_PORT` | `8080` | Public HTTP(S) proxy port |
| `RATATOSK_ADMIN_PORT` | `8081` | Admin dashboard port |
| `RATATOSK_ADMIN_HOST` | `127.0.0.1` | Admin dashboard bind address |
| `RATATOSK_ADMIN_USERNAME` | | Admin dashboard Basic Auth username |
| `RATATOSK_ADMIN_PASSWORD` | | Admin dashboard Basic Auth password |
| `RATATOSK_ADMIN_TLS_ENABLED` | `false` | Enable admin TLS; required for non-loopback binds |
| `RATATOSK_ADMIN_TLS_CERT_FILE` | | Admin TLS certificate path |
| `RATATOSK_ADMIN_TLS_KEY_FILE` | | Admin TLS private-key path |
| `RATATOSK_CONTROL_PORT` | `7000` | TCP control plane port |
| `RATATOSK_CONTROL_HOST` | `127.0.0.1` | Control-plane bind address |
| `RATATOSK_CONTROL_TOKEN` | | Inline shared control token; mutually exclusive with `RATATOSK_CONTROL_TOKEN_FILE` |
| `RATATOSK_CONTROL_TOKEN_FILE` | | Shared control-token file; mutually exclusive with `RATATOSK_CONTROL_TOKEN` |
| `RATATOSK_CONTROL_TLS_ENABLED` | `false` | Enable control-plane TLS |
| `RATATOSK_CONTROL_TLS_CERT_FILE` | | Control-plane certificate path |
| `RATATOSK_CONTROL_TLS_KEY_FILE` | | Control-plane private key path |
| `RATATOSK_TLS_ENABLED` | `false` | Enable manual TLS on the public proxy |
| `RATATOSK_TLS_CERT_FILE` | | Path to TLS certificate (PEM) |
| `RATATOSK_TLS_KEY_FILE` | | Path to TLS private key (PEM) |
| `RATATOSK_TLS_AUTO` | `false` | Enable automatic TLS via Let's Encrypt DNS-01 |
| `RATATOSK_TLS_EMAIL` | | Email for Let's Encrypt account registration |
| `RATATOSK_TLS_PROVIDER` | | DNS provider for DNS-01 challenges (e.g., `cloudflare`) |
| `RATATOSK_TLS_API_TOKEN` | | API token for the DNS provider |
| `RATATOSK_PORT_RANGE_START` | `10000` | Start of dynamic port range for TCP/UDP tunnels |
| `RATATOSK_PORT_RANGE_END` | `20000` | End of dynamic port range (exclusive) |

## Ports

| Port | Purpose |
|------|---------|
| `7000` | TCP control plane (CLI client connections) |
| `8080` | Public HTTP(S) proxy |
| `8081` | Admin dashboard and API |
| `10000-20000` | Dynamic port range for TCP/UDP tunnels |

The TCP/UDP port range is configurable via `port_range_start` and `port_range_end`. When a client requests a TCP or UDP tunnel, the server randomly allocates a port from this range. Make sure these ports are open in your firewall if you use TCP/UDP tunnels.

## Control-Plane And Admin Security

The default admin and control listeners bind to `127.0.0.1`. Ratatosk rejects unsafe control-plane combinations:

- A non-loopback `admin_host` requires admin TLS with a certificate/key pair and both `admin_username` and `admin_password`. Admin dashboard Basic Auth is separate from visitor Basic Auth on a tunnel.
- Plaintext with no control authentication is allowed only when `control_host` is a literal loopback IP such as `127.0.0.1` or `::1`. `localhost`, wildcard binds, hostnames, and non-loopback IPs require TLS.
- When `control_tls_enabled` is `true`, exactly one of `control_token` or `control_token_file` is required, even for loopback. The token is trimmed and must contain at least 32 bytes; token files are limited to 4 KiB.
- When `control_tls_enabled` is `false`, control tokens and control certificate paths must not be configured.
- CLI clients connecting remotely automatically use TLS, must provide the same control token, and always verify the server certificate. There is no insecure verification mode.

The control certificate must contain the hostname used by CLI clients. Certificate source precedence is:

1. `control_tls_cert_file` and `control_tls_key_file`, when both are set, provide a dedicated control certificate and override certificate reuse.
2. Otherwise, manual public TLS (`tls_enabled`) reuses `tls_cert_file` and `tls_key_file`.
3. Otherwise, automatic public TLS (`tls_auto`) reuses the certificate managed by CertMagic.

The public certificate can therefore protect both public HTTPS and the control listener when it covers the relay hostname. Use the dedicated control certificate fields to override either manual or CertMagic certificate reuse.

### Credential Roles

| Credential | Protects | Configuration |
|---|---|---|
| Control token | CLI-to-relay control connection | Server: exactly one of `control_token` or `control_token_file`; client: `RATATOSK_CONTROL_TOKEN` or `RATATOSK_CONTROL_TOKEN_FILE` |
| Visitor Basic Auth | Visitors to one HTTP tunnel | Client `--basic-auth user:pass`; does not authenticate the client to the relay |
| Cloudflare API token | ACME DNS-01 changes for automatic public TLS | Server `tls_api_token` or `RATATOSK_TLS_API_TOKEN`; never sent by a CLI client |

## TLS Configuration

For production deployments, enable TLS with a wildcard certificate so all tunnel subdomains are served over HTTPS.

### Obtain a Wildcard Certificate

Use a DNS-01 challenge with [certbot](https://certbot.eff.org/):

```sh
certbot certonly --manual --preferred-challenges dns \
  -d "*.tunnel.example.com" -d "tunnel.example.com"
```

### Configure the Server

```yaml
base_domain: tunnel.example.com
public_port: 443
tls_enabled: true
tls_cert_file: /etc/letsencrypt/live/tunnel.example.com/fullchain.pem
tls_key_file: /etc/letsencrypt/live/tunnel.example.com/privkey.pem
```

When TLS is enabled, the server also starts an HTTP listener on port 80 that redirects all traffic to HTTPS.

### Automatic TLS (Recommended)

Instead of managing certificates manually with certbot, the server can automatically provision and renew Let's Encrypt wildcard certificates using DNS-01 challenges. This uses [certmagic](https://github.com/caddyserver/certmagic) under the hood.

```yaml
base_domain: tunnel.example.com
public_port: 443
tls_auto: true
tls_email: admin@example.com
tls_provider: cloudflare
tls_api_token: your-cloudflare-api-token
```

This Cloudflare API token authorizes DNS changes for certificate issuance. It is not the shared control token used by CLI clients.

Or via environment variables (recommended for secrets):

```sh
export RATATOSK_TLS_AUTO=true
export RATATOSK_TLS_EMAIL=admin@example.com
export RATATOSK_TLS_PROVIDER=cloudflare
export RATATOSK_TLS_API_TOKEN=your-cloudflare-api-token
```

When `tls_auto` is enabled, the server:

1. Registers an ACME account with Let's Encrypt using the provided email.
2. Solves DNS-01 challenges via the configured DNS provider to prove domain ownership.
3. Provisions a wildcard certificate for `*.base_domain` and the base domain itself.
4. Automatically renews the certificate before it expires.
5. Listens on ports 80 (HTTP redirect) and 443 (HTTPS) automatically.

::: warning
`tls_auto` and `tls_enabled` are mutually exclusive -- use one or the other. `tls_auto` also requires a real domain (`localhost` is not supported).
:::

**Supported DNS providers:**

| Provider | `tls_provider` value | Token permissions |
|---|---|---|
| Cloudflare | `cloudflare` | Zone:DNS:Edit |

**Certificate storage:** Certificates are stored locally by certmagic. For Docker deployments, mount a persistent volume at the certmagic data directory to avoid re-issuing certificates on restart (which could hit Let's Encrypt rate limits). See the Docker Compose templates in `deploy/compose/` for an example.

::: tip
On first startup with `tls_auto`, the server may take 30-60 seconds to boot while DNS challenges propagate. Subsequent starts use the cached certificate and are instant.
:::

For a complete production setup walkthrough, see the [Deployment Guide](/guide/deployment).
