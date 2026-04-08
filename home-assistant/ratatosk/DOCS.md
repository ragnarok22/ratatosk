# Ratatosk Tunnel - Home Assistant Add-on

Expose your Home Assistant instance to the internet securely using a Ratatosk tunnel, without messing with router port forwarding or complex reverse proxy configurations.

## Prerequisites

You need a running Ratatosk relay server on a publicly accessible VPS. See the [deployment guide](https://github.com/ragnarok22/ratatosk/blob/main/docs/guide/deployment.md) for setup instructions.

## Configuration

| Option | Required | Default | Description |
|--------|----------|---------|-------------|
| `server` | Yes | — | Relay server address in `host:port` format (e.g., `tunnel.example.com:7000`) |
| `port` | No | `8123` | Local port to expose (Home Assistant default is 8123) |
| `basic_auth` | No | — | Require HTTP Basic Auth for tunnel visitors (format: `user:pass`) |
| `streamer` | No | `false` | Redact sensitive data (IPs, tokens) from logs for streaming |

## Example

To expose your Home Assistant dashboard running on port 8123:

1. Install the add-on
2. Set `server` to your relay server address (e.g., `tunnel.example.com:7000`)
3. Start the add-on
4. Your HA instance will be available at the generated subdomain URL (e.g., `https://golden-bifrost-004721.tunnel.example.com`)

## Support

For issues and feature requests, visit the [GitHub repository](https://github.com/ragnarok22/ratatosk/issues).
