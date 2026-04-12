# Homelab & Smart Home

Ratatosk is a natural fit for homelabs — expose local services to the internet without touching router port forwarding or setting up complex reverse proxy chains.

## Prerequisites

Before setting up the client, you need a running Ratatosk relay server on a public VPS. See the [Deployment Guide](./deployment.md) for full instructions.

## Docker Compose

Ready-to-use Docker Compose templates are provided in [`deploy/compose/`](https://github.com/ragnarok22/ratatosk/tree/main/deploy/compose). Copy the `.env.example` file and adjust values for your setup.

### Server (VPS)

Deploy the relay server on your public VPS:

```sh
cd deploy/compose
cp .env.example .env
# Edit .env: set RATATOSK_BASE_DOMAIN, TLS settings, etc.
docker compose -f server.docker-compose.yml up -d
```

### Client (Homelab)

Run the CLI client on your homelab machine to create a tunnel:

```sh
cd deploy/compose
cp .env.example .env
# Edit .env: set RATATOSK_SERVER to your relay address
docker compose -f client.docker-compose.yml up -d
```

The client uses `network_mode: host` so it can reach local services on the host machine. On Docker Desktop (Mac/Windows), see the comments in the compose file for alternatives using `host.docker.internal`.

### Full Stack (Testing)

For local development and testing with both server and client:

```sh
docker compose -f full-stack.docker-compose.yml up --build
```

### CasaOS & Portainer

The compose templates include CasaOS-compatible metadata (`x-casaos:` extension) and Portainer-friendly labels. They work natively with both platforms — just import the compose file through the platform's UI.

## Home Assistant

Ratatosk supports Home Assistant through two mechanisms depending on your installation type.

### HACS Integration (Docker / HA Core)

If you run Home Assistant Container (Docker) or HA Core, install the Ratatosk integration via [HACS](https://hacs.xyz/):

1. Open HACS in your Home Assistant instance
2. Click the menu (top right) and select **Custom repositories**
3. Add `https://github.com/ragnarok22/ratatosk` with type **Integration**
4. Search for "Ratatosk" in HACS and install it
5. Restart Home Assistant
6. Go to **Settings > Devices & services > Add Integration** and search for "Ratatosk"
7. Enter the host and port of your running Ratatosk inspector (default: `127.0.0.1:4040`)

The integration monitors a running Ratatosk CLI instance via its inspector API and exposes:

- **Connected** — binary sensor showing tunnel connectivity
- **Request count** — number of proxied requests
- **Last request** — timestamp of the most recent request

::: tip
Run the Ratatosk CLI as a sidecar Docker container on the same network as Home Assistant. Use the container name as the host in the integration config (e.g., `ratatosk:4040`).
:::

### Add-on (HA OS / HA Supervised)

If you run Home Assistant OS or HA Supervised, install Ratatosk as an add-on:

1. In Home Assistant, go to **Settings > Add-ons > Add-on Store**
2. Click the overflow menu (top right) and select **Repositories**
3. Add the repository URL: `https://github.com/ragnarok22/ratatosk`
4. Find **Ratatosk Tunnel** in the store and click **Install**

#### Add-on Configuration

| Option | Required | Default | Description |
|--------|----------|---------|-------------|
| `server` | Yes | — | Relay server address (e.g., `tunnel.example.com:7000`) |
| `port` | No | `8123` | Local port to expose (HA default is 8123) |
| `basic_auth` | No | — | HTTP Basic Auth credentials (`user:pass`) |
| `streamer` | No | `false` | Redact sensitive data from logs |

#### Example

To expose your Home Assistant dashboard:

1. Set `server` to your relay address (e.g., `tunnel.example.com:7000`)
2. Leave `port` at `8123` (the HA default)
3. Optionally set `basic_auth` to protect the tunnel (e.g., `admin:secret`)
4. Start the add-on

Your Home Assistant instance will be available at the generated tunnel URL (e.g., `https://golden-bifrost-004721.tunnel.example.com`).

## CLI Client with Environment Variables

For non-Docker homelab setups, the CLI client supports the `RATATOSK_SERVER` environment variable as an alternative to the `--server` flag:

```sh
export RATATOSK_SERVER=tunnel.example.com:7000
ratatosk --port 8123
```

This is useful for systemd services, cron jobs, or any environment where passing flags is inconvenient. See the [CLI Commands](../reference/cli-commands.md) reference for all available options.
