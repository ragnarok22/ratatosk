# Deployment Guide

Configuring the public infrastructure — especially Wildcard certificates — is usually where most developers get stuck. This is a step-by-step survival guide to getting Ratatosk running on your VPS without losing your mind.

## Prerequisites

- A VPS with a public IP address (e.g. `1.2.3.4`)
- A domain you control (e.g. `yourdomain.com`)
- SSH access to the VPS
- For manual TLS: `certbot` installed on the VPS (`sudo apt install certbot` on Debian/Ubuntu)
- For automatic TLS: a Cloudflare API token with Zone:DNS:Edit permissions

## Step 1: Configure DNS Records

Go to your domain's admin panel (Cloudflare, Namecheap, Route53, etc.) and create two records pointing to your VPS public IP.

Assuming you want your proxy to live at `tunnel.yourdomain.com`:

**A Record** (for the admin panel and base domain):

| Field | Value |
|---|---|
| Type | A |
| Name/Host | `tunnel` |
| Destination/IP | `1.2.3.4` |

**Wildcard Record** (for your local client tunnels):

| Field | Value |
|---|---|
| Type | A (or CNAME) |
| Name/Host | `*.tunnel` |
| Destination/IP | `1.2.3.4` (or pointing to `tunnel.yourdomain.com`) |

> **Cloudflare users:** Make sure to turn off the "Orange Cloud" (set Proxy status to **DNS Only**) for these records. WebSocket traffic and raw TCP tunnels can conflict with Cloudflare's free-tier proxy rules.

Verify your DNS is working before continuing:

```sh
dig tunnel.yourdomain.com        # should resolve to your VPS IP
dig test.tunnel.yourdomain.com   # should also resolve to your VPS IP
```

## Step 2: Generate the Wildcard SSL Certificate

SSH into your VPS. You can't use the standard HTTP challenge here because you need to validate a wildcard (`*`) domain — this requires a DNS challenge.

```sh
sudo certbot certonly \
  --manual \
  --preferred-challenges dns \
  -d "tunnel.yourdomain.com" \
  -d "*.tunnel.yourdomain.com"
```

What happens next:

1. Certbot will pause and ask you to create a **TXT record** in your DNS provider. It will give you a specific name (usually `_acme-challenge.tunnel`) and a long alphanumeric value.
2. Go to your DNS provider and create that TXT record.
3. Wait a couple of minutes for DNS propagation before pressing Enter in the Certbot terminal.
4. If everything checks out, Certbot will tell you your certificates are saved at `/etc/letsencrypt/live/tunnel.yourdomain.com/`.

> **Pro tip:** If you use Cloudflare, install the `certbot-dns-cloudflare` plugin. This automates the TXT record step using an API key and allows certificates to auto-renew every 3 months without manual intervention:
>
> ```sh
> sudo apt install python3-certbot-dns-cloudflare
>
> # Create /etc/letsencrypt/cloudflare.ini with:
> #   dns_cloudflare_api_token = YOUR_API_TOKEN
> sudo chmod 600 /etc/letsencrypt/cloudflare.ini
>
> sudo certbot certonly \
>   --dns-cloudflare \
>   --dns-cloudflare-credentials /etc/letsencrypt/cloudflare.ini \
>   -d "tunnel.yourdomain.com" \
>   -d "*.tunnel.yourdomain.com"
> ```

## Alternative: Automatic TLS (Skip Steps 2 and Cert Config)

Instead of managing certificates manually with certbot, Ratatosk can automatically provision and renew Let's Encrypt wildcard certificates using DNS-01 challenges. This is the recommended approach -- no certbot required.

You just need a Cloudflare API token with **Zone:DNS:Edit** permissions for your domain. Create one at [Cloudflare Dashboard > API Tokens](https://dash.cloudflare.com/profile/api-tokens).

Skip Step 2 entirely and use this config in Step 4 instead:

```yaml
base_domain: tunnel.yourdomain.com
public_port: 443
admin_port: 8081
control_port: 7000

tls_auto: true
tls_email: you@yourdomain.com
tls_provider: cloudflare
tls_api_token: your-cloudflare-api-token
```

Or via environment variables (recommended for the API token):

```sh
export RATATOSK_TLS_AUTO=true
export RATATOSK_TLS_EMAIL=you@yourdomain.com
export RATATOSK_TLS_PROVIDER=cloudflare
export RATATOSK_TLS_API_TOKEN=your-cloudflare-api-token
```

On first startup, the server will solve DNS challenges and provision the certificate automatically. This may take 30-60 seconds. Subsequent starts use the cached certificate. Certificates are stored under `$XDG_DATA_HOME/certmagic` (defaults to `~/.local/share/certmagic`).

::: tip
With automatic TLS, you don't need to install certbot, set filesystem ACLs for certificate files, or worry about renewal -- it's all handled for you.
:::

## Step 3: Build and Upload the Binary

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

## Step 4: Configure Ratatosk on the VPS

Create the config directory and file:

```sh
sudo mkdir -p /etc/ratatosk /var/log/ratatosk /var/lib/ratatosk
```

Create `/etc/ratatosk/ratatosk.yaml` with your production values.

**With automatic TLS (recommended):**

```yaml
base_domain: tunnel.yourdomain.com
public_port: 443
admin_port: 8081
control_port: 7000

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
control_port: 7000

tls_enabled: true
tls_cert_file: /etc/letsencrypt/live/tunnel.yourdomain.com/fullchain.pem
tls_key_file: /etc/letsencrypt/live/tunnel.yourdomain.com/privkey.pem
```

## Step 5: Set Up the Systemd Service

Create a dedicated system user:

```sh
sudo useradd --system --no-create-home --shell /usr/sbin/nologin ratatosk
sudo chown -R ratatosk:ratatosk /etc/ratatosk /var/log/ratatosk
```

Let's Encrypt certificates are only readable by root by default. Grant the `ratatosk` user read access:

```sh
sudo setfacl -m u:ratatosk:rX /etc/letsencrypt/live /etc/letsencrypt/archive
sudo setfacl -m u:ratatosk:r /etc/letsencrypt/live/tunnel.yourdomain.com/fullchain.pem
sudo setfacl -m u:ratatosk:r /etc/letsencrypt/live/tunnel.yourdomain.com/privkey.pem
```

Copy the service file and start it:

```sh
sudo cp deploy/ratatosk.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable ratatosk
sudo systemctl start ratatosk
```

Check that it's running:

```sh
sudo systemctl status ratatosk
```

If the status shows **active (running)**, open `https://tunnel.yourdomain.com:8081` in your browser. You should see the React dashboard running in the cloud with a green padlock.

To follow logs in real time:

```sh
sudo journalctl -u ratatosk -f
```

## Step 6: The Moment of Truth

With the server running on your VPS, connect from your local machine:

```sh
ratatosk --port 3000
```

You should get a tunnel URL like `https://golden-bifrost-004721.tunnel.yourdomain.com` pointing to your `localhost:3000`.

## Deploying with Docker (Alternative)

If you prefer Docker over Systemd:

```sh
docker build -f deploy/Dockerfile.server -t ratatosk-server .

docker run -d \
  --name ratatosk \
  --restart always \
  -v /etc/ratatosk/ratatosk.yaml:/etc/ratatosk/ratatosk.yaml:ro \
  -v /etc/letsencrypt:/etc/letsencrypt:ro \
  -p 80:80 \
  -p 443:443 \
  -p 7000:7000 \
  -p 8081:8081 \
  ratatosk-server
```

Or using environment variables instead of a config file:

```sh
docker run -d \
  --name ratatosk \
  --restart always \
  -e RATATOSK_BASE_DOMAIN=tunnel.yourdomain.com \
  -e RATATOSK_PUBLIC_PORT=443 \
  -e RATATOSK_TLS_ENABLED=true \
  -e RATATOSK_TLS_CERT_FILE=/etc/letsencrypt/live/tunnel.yourdomain.com/fullchain.pem \
  -e RATATOSK_TLS_KEY_FILE=/etc/letsencrypt/live/tunnel.yourdomain.com/privkey.pem \
  -v /etc/letsencrypt:/etc/letsencrypt:ro \
  -p 80:80 \
  -p 443:443 \
  -p 7000:7000 \
  -p 8081:8081 \
  ratatosk-server
```

## Deploying with Docker Compose

Docker Compose templates are available in [`deploy/compose/`](https://github.com/ragnarok22/ratatosk/tree/main/deploy/compose) for both server and client deployments.

### Server

```sh
cd deploy/compose
cp .env.example .env
# Edit .env with your domain, ports, and TLS settings
docker compose -f server.docker-compose.yml up -d
```

### Client

```sh
cd deploy/compose
cp .env.example .env
# Set RATATOSK_SERVER to your relay address
docker compose -f client.docker-compose.yml up -d
```

The client uses `network_mode: host` on Linux so it can reach local services. On Docker Desktop (Mac/Windows), see the comments in the compose file for alternatives.

### Full Stack (Development)

For local testing with both server and client:

```sh
docker compose -f full-stack.docker-compose.yml up --build
```

## Home Assistant Add-on

If you run Home Assistant, you can install Ratatosk as an add-on to expose your dashboard without port forwarding.

1. In Home Assistant, go to **Settings > Add-ons > Add-on Store**
2. Click the menu (top right) and select **Repositories**
3. Add: `https://github.com/ragnarok22/ratatosk`
4. Install "Ratatosk Tunnel" from the store
5. Configure the `server` option with your relay server address (e.g., `tunnel.yourdomain.com:7000`)
6. Start the add-on

The add-on exposes your HA instance (default port 8123) through the tunnel. See [`home-assistant/ratatosk/DOCS.md`](https://github.com/ragnarok22/ratatosk/blob/main/home-assistant/ratatosk/DOCS.md) for full configuration options.

## Troubleshooting

**"bind: permission denied" on port 443 or 80**

The Systemd unit grants `CAP_NET_BIND_SERVICE` so the `ratatosk` user can bind privileged ports. If running manually, either use `sudo` or bind to a high port and front it with a reverse proxy.

**Certbot fails the DNS challenge**

DNS propagation can take time. Wait 2-5 minutes after creating the TXT record before pressing Enter. You can verify propagation with:

```sh
dig TXT _acme-challenge.tunnel.yourdomain.com
```

**Dashboard loads but tunnels don't connect**

Make sure port `7000` (TCP control plane) is open in your VPS firewall:

```sh
sudo ufw allow 7000/tcp   # if using ufw
```

**Certificate renewal**

If using `tls_auto`, certificates are renewed automatically by the server -- no action needed. If using manual TLS with certbot, Let's Encrypt certificates expire every 90 days. If you used the Cloudflare DNS plugin, `certbot renew` runs automatically via a systemd timer. For manual DNS challenges, you'll need to repeat the process. After renewal, restart the server:

```sh
sudo systemctl restart ratatosk
```

**Automatic TLS: "first startup is slow"**

When using `tls_auto`, the first startup may take 30-60 seconds while DNS challenges propagate. This is normal. Subsequent starts use the cached certificate and are instant. Check logs with `journalctl -u ratatosk -f` to see the progress.
