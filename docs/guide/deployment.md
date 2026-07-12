# Deployment Guide

This guide walks you through deploying the Ratatosk relay server on a public VPS. By the end, you'll have a fully working tunnel server with automatic HTTPS.

## The Golden Path (Recommended)

The fastest way to go from zero to a running tunnel server. This uses the automated installer and interactive setup wizard.

::: info Prerequisites
- A VPS with a public IP address (e.g., `1.2.3.4`)
- A domain you control (e.g., `yourdomain.com`)
- SSH access to the VPS
- A Cloudflare account with an API token ([how to create one](#cloudflare-api-token-setup))
:::

### 1. Configure DNS Records

Go to your domain's DNS settings and create two records pointing to your VPS IP.

Assuming your tunnel domain is `tunnel.yourdomain.com`:

| Type | Name/Host | Value |
|------|-----------|-------|
| A | `tunnel` | `1.2.3.4` |
| A (or CNAME) | `*.tunnel` | `1.2.3.4` (or `tunnel.yourdomain.com`) |

::: warning Cloudflare Users
Set Proxy status to **DNS Only** (grey cloud) for both records. Cloudflare's proxy can interfere with WebSocket traffic and raw TCP tunnels.
:::

Verify your DNS is working:

```sh
dig tunnel.yourdomain.com        # should resolve to your VPS IP
dig test.tunnel.yourdomain.com   # should also resolve to your VPS IP
```

### 2. Install Ratatosk

SSH into your VPS and run the one-line installer:

```sh
curl -sSL https://raw.githubusercontent.com/ragnarok22/ratatosk/main/deploy/install.sh | sudo bash
```

This downloads the latest binary, creates a systemd service, sets up the `ratatosk` system user, and prepares the directory structure.

::: tip
The script is idempotent — re-run it anytime to upgrade the binary. Your existing config will not be overwritten.
:::

### 3. Run the Setup Wizard

Generate your config file interactively:

```sh
sudo -u ratatosk sh -c 'cd /etc/ratatosk && ratatosk-server init'
```

The wizard will ask you for:

- Your base domain (e.g., `tunnel.yourdomain.com`)
- Whether to enable automatic wildcard TLS via Let's Encrypt (recommended)
- Your email address for certificate expiry notices
- Your Cloudflare API token
- Whether to expose the control plane to remote clients

When remote control is enabled, the wizard configures verified control TLS and generates a shared token in `/etc/ratatosk/control-token`. The config is saved to `/etc/ratatosk/ratatosk.yaml`. Ensure the service account can read both files:

```sh
sudo chown ratatosk:ratatosk /etc/ratatosk/ratatosk.yaml /etc/ratatosk/control-token
sudo chmod 600 /etc/ratatosk/ratatosk.yaml /etc/ratatosk/control-token
```

### 4. Start the Server

```sh
sudo systemctl enable --now ratatosk
```

Check that it's running:

```sh
sudo systemctl status ratatosk
```

To follow logs in real time:

```sh
sudo journalctl -u ratatosk -f
```

::: tip
On first startup with automatic TLS, certificate provisioning may take 30–60 seconds while DNS challenges propagate. Subsequent starts use the cached certificate and are instant.
:::

### 5. Connect Your First Tunnel

Securely copy the generated `/etc/ratatosk/control-token` to your local machine. Keep it mode `0600`, then point the CLI at the file and connect:

```sh
export RATATOSK_CONTROL_TOKEN_FILE="$HOME/.config/ratatosk/control-token"
ratatosk --server tunnel.yourdomain.com:7000 --port 3000
```

Remote addresses automatically use verified TLS. With automatic public TLS, the control listener reuses the CertMagic certificate for `tunnel.yourdomain.com`; no separate control certificate is required. The client does not accept a `--token` flag and does not provide an insecure certificate-verification mode.

You should get a tunnel URL like `https://golden-bifrost-004721.tunnel.yourdomain.com` pointing to your `localhost:3000`.

## Cloudflare API Token Setup

Automatic TLS requires a Cloudflare API token with specific permissions to create DNS records for the ACME DNS-01 challenge. This is a server-side DNS credential, not the shared control token and not visitor Basic Auth.

::: warning Required Permission
The API token **must** have **Zone:DNS:Edit** permission for your domain. Tokens with only read access will not work.
:::

To create a token:

1. Go to [Cloudflare Dashboard > API Tokens](https://dash.cloudflare.com/profile/api-tokens)
2. Click **Create Token**
3. Use the **Edit zone DNS** template, or create a custom token with:
   - **Permissions:** Zone > DNS > Edit
   - **Zone Resources:** Include > Specific zone > your domain
4. Copy the token and paste it into the setup wizard (or your config file)

The token is stored in `/etc/ratatosk/ratatosk.yaml` with restricted file permissions (mode `0600`). For additional security, you can set it via the `RATATOSK_TLS_API_TOKEN` environment variable instead.

## Docker Deployment

If you prefer Docker over systemd, you can deploy Ratatosk as a container.

### Docker Run

```sh
docker build -f deploy/Dockerfile.server -t ratatosk-server .

# Generates 32 random bytes encoded as 64 hexadecimal characters.
export RATATOSK_CONTROL_TOKEN="$(openssl rand -hex 32)"

docker run -d \
  --name ratatosk \
  --restart always \
  -e RATATOSK_BASE_DOMAIN=tunnel.yourdomain.com \
  -e RATATOSK_PUBLIC_PORT=443 \
  -e RATATOSK_TLS_AUTO=true \
  -e RATATOSK_TLS_EMAIL=you@yourdomain.com \
  -e RATATOSK_TLS_PROVIDER=cloudflare \
  -e RATATOSK_TLS_API_TOKEN=your-cloudflare-api-token \
  -e RATATOSK_CONTROL_HOST=0.0.0.0 \
  -e RATATOSK_CONTROL_TOKEN="$RATATOSK_CONTROL_TOKEN" \
  -e RATATOSK_CONTROL_TLS_ENABLED=true \
  -e XDG_DATA_HOME=/data \
  -v ratatosk-certs:/data/certmagic \
  -p 80:80 \
  -p 443:443 \
  -p 7000:7000 \
  ratatosk-server
```

This example leaves the admin dashboard on its default loopback bind and reuses the CertMagic public certificate for the control listener. To avoid an inline server secret, mount a read-only token file and set `RATATOSK_CONTROL_TOKEN_FILE` instead. Never configure both token variables.

### Docker Compose

Docker Compose templates are available in [`deploy/compose/`](https://github.com/ragnarok22/ratatosk/tree/main/deploy/compose).

**Server (VPS deployment):**

```sh
cd deploy/compose
cp .env.example .env
# Edit .env with your domain, ports, TLS settings, and a token from: openssl rand -hex 32
docker compose -f server.docker-compose.yml up -d
```

**Client (local machine / homelab):**

```sh
cd deploy/compose
cp .env.example .env
# Set RATATOSK_SERVER and the matching RATATOSK_CONTROL_TOKEN
docker compose -f client.docker-compose.yml up -d
```

The client uses `network_mode: host` on Linux so it can reach local services. On Docker Desktop (Mac/Windows), see the comments in the compose file for alternatives.

**Full Stack (development):**

```sh
docker compose -f full-stack.docker-compose.yml up --build
```

## Home Assistant

Ratatosk integrates with Home Assistant in two ways:

- **HACS Integration** — for HA Container (Docker) and HA Core. Monitors a running Ratatosk instance and exposes tunnel status as HA entities.
- **Supervisor Add-on** — for HA OS and HA Supervised. Runs the tunnel client directly as an add-on.

See the [Homelab & Smart Home](./homelab.md#home-assistant) guide for full setup instructions.

## Advanced / Manual Setup

::: info
This section is for users who cannot use the automated installer or setup wizard — for example, if you're using a DNS provider other than Cloudflare, or need manual certificate management. Most users should follow [The Golden Path](#the-golden-path-recommended) instead.
:::

### Manual TLS with Certbot

If you can't use automatic TLS, you can provision wildcard certificates manually with certbot using DNS-01 challenges.

```sh
sudo certbot certonly \
  --manual \
  --preferred-challenges dns \
  -d "tunnel.yourdomain.com" \
  -d "*.tunnel.yourdomain.com"
```

Certbot will ask you to create a TXT record (`_acme-challenge.tunnel`) in your DNS provider. Create the record, wait 2–5 minutes for propagation, then press Enter.

Certificates are saved to `/etc/letsencrypt/live/tunnel.yourdomain.com/`.

**Cloudflare DNS plugin (automates renewal):**

```sh
sudo apt install python3-certbot-dns-cloudflare

# Create /etc/letsencrypt/cloudflare.ini with:
#   dns_cloudflare_api_token = YOUR_API_TOKEN
sudo chmod 600 /etc/letsencrypt/cloudflare.ini

sudo certbot certonly \
  --dns-cloudflare \
  --dns-cloudflare-credentials /etc/letsencrypt/cloudflare.ini \
  -d "tunnel.yourdomain.com" \
  -d "*.tunnel.yourdomain.com"
```

### Build from Source

On your local machine, cross-compile for Linux:

```sh
make build-dashboard
GOOS=linux GOARCH=amd64 go build -o ratatosk-server ./cmd/server
```

Copy the binary to your VPS:

```sh
scp ratatosk-server your-user@1.2.3.4:/tmp/
```

On the VPS, move it into place:

```sh
sudo mv /tmp/ratatosk-server /usr/local/bin/ratatosk-server
sudo chmod +x /usr/local/bin/ratatosk-server
```

### Manual Configuration

Create directories and the config file:

```sh
sudo mkdir -p /etc/ratatosk /var/log/ratatosk /var/lib/ratatosk
```

Create `/etc/ratatosk/ratatosk.yaml`:

**With automatic TLS:**

Generate a control token before writing the config. The token must contain at least 32 bytes; this command generates 32 random bytes as 64 hexadecimal characters:

```sh
openssl rand -hex 32 | sudo tee /etc/ratatosk/control-token >/dev/null
sudo chmod 600 /etc/ratatosk/control-token
```

Securely copy the same token file to every authorized client. Do not reuse the Cloudflare API token as the control token.

Automatic public TLS is provisioned before listeners start. The remote control listener can reuse the CertMagic certificate for the relay hostname, so it does not need dedicated certificate files. This does not apply to a remotely exposed admin listener, which requires its own admin TLS configuration; the example keeps the admin listener on loopback.

```yaml
base_domain: tunnel.yourdomain.com
public_port: 443
admin_port: 8081
admin_host: 127.0.0.1
control_port: 7000
control_host: 0.0.0.0
control_token_file: /etc/ratatosk/control-token
control_tls_enabled: true

tls_auto: true
tls_email: you@yourdomain.com
tls_provider: cloudflare
tls_api_token: your-cloudflare-api-token
```

**With manual TLS (certbot):**

```yaml
base_domain: tunnel.yourdomain.com
public_port: 443
admin_port: 8081
admin_host: 127.0.0.1
control_port: 7000
control_host: 0.0.0.0
control_token_file: /etc/ratatosk/control-token
control_tls_enabled: true

tls_enabled: true
tls_cert_file: /etc/letsencrypt/live/tunnel.yourdomain.com/fullchain.pem
tls_key_file: /etc/letsencrypt/live/tunnel.yourdomain.com/privkey.pem
```

The manual public certificate is reused for the control listener because the dedicated control fields are omitted. Certificate selection is: dedicated `control_tls_cert_file`/`control_tls_key_file` first, then the manual public certificate, then the CertMagic certificate. Set both dedicated control fields to override public-certificate reuse.

### Systemd Service (Manual)

Create a dedicated system user:

```sh
sudo useradd --system --no-create-home --shell /usr/sbin/nologin ratatosk
sudo chown -R ratatosk:ratatosk /etc/ratatosk /var/log/ratatosk
```

This ownership step also makes the control-token file readable by the service while keeping it private from other users.

If using manual TLS, grant the `ratatosk` user read access to certificates:

```sh
sudo setfacl -m u:ratatosk:rX /etc/letsencrypt/live /etc/letsencrypt/archive
sudo setfacl -m u:ratatosk:r /etc/letsencrypt/live/tunnel.yourdomain.com/fullchain.pem
sudo setfacl -m u:ratatosk:r /etc/letsencrypt/live/tunnel.yourdomain.com/privkey.pem
```

Install the systemd service:

```sh
sudo cp deploy/ratatosk.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ratatosk
```

### Certificate Renewal

- **Automatic TLS (`tls_auto`):** Certificates are renewed automatically — no action needed.
- **Certbot with DNS plugin:** Renewals happen automatically via a systemd timer.
- **Certbot with manual DNS:** You must repeat the DNS challenge process every 90 days. After renewal, restart the server:

```sh
sudo systemctl restart ratatosk
```

## Troubleshooting

**"bind: permission denied" on port 443 or 80**

The systemd unit grants `CAP_NET_BIND_SERVICE` so the `ratatosk` user can bind privileged ports. If running manually, either use `sudo` or bind to a high port and front it with a reverse proxy.

**Setup wizard fails with "permission denied"**

The wizard writes to `/etc/ratatosk/ratatosk.yaml` when run as root. Make sure to use `sudo`:

```sh
sudo -u ratatosk sh -c 'cd /etc/ratatosk && ratatosk-server init'
```

If running without root, the config is written to `./ratatosk.yaml` in the current directory.

**Certbot fails the DNS challenge**

DNS propagation can take time. Wait 2–5 minutes after creating the TXT record before pressing Enter. Verify propagation with:

```sh
dig TXT _acme-challenge.tunnel.yourdomain.com
```

**Dashboard loads but tunnels don't connect**

Make sure port `7000` (TCP control plane) is open in your VPS firewall:

```sh
sudo ufw allow 7000/tcp   # if using ufw
```

Also verify that control TLS is enabled, the client has exactly one matching token source, and the relay certificate is valid for the address the client uses. For a private CA, configure `--ca`; use `--server-name` only when the dial address differs from the certificate name. Do not bypass certificate verification.

**Automatic TLS: first startup is slow**

When using `tls_auto`, the first startup may take 30–60 seconds while DNS challenges propagate. This is normal. Subsequent starts use the cached certificate and are instant. Check logs with `journalctl -u ratatosk -f` to see progress.
