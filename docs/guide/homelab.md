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
# Edit .env: set the domain, TLS settings, and a 32-byte-or-longer control token.
docker compose -f server.docker-compose.yml up -d
```

### Client (Homelab)

Run the CLI client on your homelab machine to create a tunnel:

```sh
cd deploy/compose
cp .env.example .env
# Edit .env: set RATATOSK_SERVER and the same RATATOSK_CONTROL_TOKEN as the relay.
docker compose -f client.docker-compose.yml up -d
```

The client uses `network_mode: host` so it can reach local services on the host machine. Remote relay addresses automatically use verified TLS; `RATATOSK_CONTROL_TLS_ENABLED` is only needed to force TLS for a loopback address. On Docker Desktop (Mac/Windows), see the comments in the compose file for alternatives using `host.docker.internal`.

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
Run the Ratatosk CLI as a sidecar Docker container on the same network as Home Assistant. Start it with `--inspector-host 0.0.0.0` so the inspector API is reachable from other containers, then use the container name as the host in the integration config (e.g., `ratatosk:4040`).

```sh
RATATOSK_CONTROL_TOKEN_FILE=/run/secrets/control-token \
  ratatosk --port 8123 --server tunnel.example.com:7000 --inspector-host 0.0.0.0
```
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
| `control_token` | Yes for remote relays | — | Shared control token, at least 32 bytes |
| `control_tls` | No | `true` | Force verified control TLS; remote addresses already enable it automatically |
| `control_ca_file` | No | — | Private CA certificate path |
| `control_server_name` | No | — | Certificate name when it differs from the relay address |
| `port` | No | `8123` | Local port to expose (HA default is 8123) |
| `basic_auth` | No | — | Basic Auth credentials for HTTP tunnel visitors (`user:pass`) |
| `streamer` | No | `false` | Redact sensitive data from logs |

#### Example

To expose your Home Assistant dashboard:

1. Set `server` to your relay address (e.g., `tunnel.example.com:7000`)
2. Set `control_token` to the relay's shared token
3. Leave `port` at `8123` (the HA default)
4. Optionally set `basic_auth` to protect the tunnel from visitors (e.g., `admin:secret`)
5. Start the add-on

Your Home Assistant instance will be available at the generated tunnel URL (e.g., `https://golden-bifrost-004721.tunnel.example.com`).

## CLI Client with Environment Variables

For non-Docker homelab setups, the CLI client supports `RATATOSK_SERVER` and file-backed control tokens:

```sh
export RATATOSK_SERVER=tunnel.example.com:7000
export RATATOSK_CONTROL_TOKEN_FILE=/etc/ratatosk/control-token
ratatosk --port 8123
```

The token file should be readable only by the account running Ratatosk. Use exactly one of `RATATOSK_CONTROL_TOKEN_FILE` or `RATATOSK_CONTROL_TOKEN`; the CLI has no `--token` flag. This control token authenticates the homelab client to the relay. It is separate from optional visitor Basic Auth and from the server's Cloudflare API token. See the [CLI Commands](../reference/cli-commands.md) reference for all available options.
