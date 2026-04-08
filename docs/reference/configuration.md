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

## Ports

| Port | Purpose |
|------|---------|
| `7000` | TCP control plane (CLI client connections) |
| `8080` | Public HTTP(S) proxy |
| `8081` | Admin dashboard and API |

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
