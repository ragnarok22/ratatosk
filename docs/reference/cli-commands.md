# CLI Commands

The Ratatosk CLI runs on your local machine and manages tunnel connections to the relay server.

## Usage

```sh
ratatosk [command] [flags]
```

## Commands

| Command | Description |
|---------|-------------|
| `ratatosk --port <port>` | Expose a local service (default: 3000) |
| `ratatosk version` | Print the CLI version |
| `ratatosk self-update` | Check for updates and self-update |

## Flags

### `--port`

The local port to expose through the tunnel.

- **Type:** integer
- **Default:** `3000`

```sh
ratatosk --port 8080
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
