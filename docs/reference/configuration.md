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
admin_port: 8081             # Admin dashboard port
control_port: 7000           # TCP control plane port
tls_enabled: false           # Enable TLS on the public proxy
tls_cert_file: ""            # Path to TLS certificate (PEM)
tls_key_file: ""             # Path to TLS private key (PEM)
port_range_start: 10000      # Start of dynamic port range for TCP/UDP tunnels
port_range_end: 20000        # End of dynamic port range (exclusive)
```

## Environment Variables

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

For a complete production setup walkthrough, see the [Deployment Guide](/guide/deployment).
