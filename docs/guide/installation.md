# Installation

Ratatosk has two components to install: the **relay server** (runs on a public VPS) and the **CLI client** (runs on your local machine).

## Install the CLI

The CLI client opens a persistent, multiplexed connection to the relay server and forwards tunneled requests to a local port.

### Homebrew (macOS / Linux)

```sh
brew tap ragnarok22/tap
brew install ratatosk
```

### Download a Binary

Grab the latest release for your platform from the [Releases](https://github.com/ragnarok22/ratatosk/releases) page, or use the commands below:

```sh
# macOS (Apple Silicon)
curl -Lo ratatosk https://github.com/ragnarok22/ratatosk/releases/latest/download/ratatosk-cli-darwin-arm64
chmod +x ratatosk && sudo mv ratatosk /usr/local/bin/

# macOS (Intel)
curl -Lo ratatosk https://github.com/ragnarok22/ratatosk/releases/latest/download/ratatosk-cli-darwin-amd64
chmod +x ratatosk && sudo mv ratatosk /usr/local/bin/

# Linux (amd64)
curl -Lo ratatosk https://github.com/ragnarok22/ratatosk/releases/latest/download/ratatosk-cli-linux-amd64
chmod +x ratatosk && sudo mv ratatosk /usr/local/bin/
```

On Windows, download `ratatosk-cli-windows-amd64.exe` from the Releases page.

### Build from Source

```sh
git clone https://github.com/ragnarok22/ratatosk.git
cd ratatosk
make build
sudo cp bin/cli /usr/local/bin/ratatosk
```

## Install the Server

The relay server accepts CLI client connections and routes public HTTP traffic to the correct tunnel.

### Docker (Recommended)

```sh
docker run -d --name ratatosk \
  -p 7000:7000 -p 8080:8080 -p 8081:8081 \
  ghcr.io/ragnarok22/ratatosk-server
```

To pass a config file:

```sh
docker run -d --name ratatosk \
  -v /path/to/ratatosk.yaml:/etc/ratatosk/ratatosk.yaml:ro \
  -p 7000:7000 -p 443:443 -p 8081:8081 \
  ghcr.io/ragnarok22/ratatosk-server
```

### Build from Source

```sh
git clone https://github.com/ragnarok22/ratatosk.git
cd ratatosk
make build
sudo cp bin/server /usr/local/bin/ratatosk-server
```

## Next Steps

Once both components are installed, head to the [Getting Started](/guide/getting-started) guide to create your first tunnel.
