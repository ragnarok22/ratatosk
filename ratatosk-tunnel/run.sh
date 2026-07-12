#!/usr/bin/with-contenv bashio

set -euo pipefail

SERVER=$(bashio::config 'server')
CONTROL_TOKEN=$(bashio::config 'control_token')
CONTROL_TLS=$(bashio::config 'control_tls')
CONTROL_CA_FILE=$(bashio::config 'control_ca_file')
CONTROL_SERVER_NAME=$(bashio::config 'control_server_name')
PORT=$(bashio::config 'port')
BASIC_AUTH=$(bashio::config 'basic_auth')
STREAMER=$(bashio::config 'streamer')

if [ -z "$SERVER" ]; then
  bashio::log.fatal "The 'server' option is required. Set it to your Ratatosk relay server address (e.g., tunnel.example.com:7000)."
  exit 1
fi

ARGS=(--server "$SERVER" --port "$PORT")

if [ -n "$CONTROL_TOKEN" ]; then
  ARGS+=(--token "$CONTROL_TOKEN")
fi

if [ "$CONTROL_TLS" = "true" ]; then
  ARGS+=(--tls)
fi

if [ -n "$CONTROL_CA_FILE" ]; then
  ARGS+=(--ca "$CONTROL_CA_FILE")
fi

if [ -n "$CONTROL_SERVER_NAME" ]; then
  ARGS+=(--server-name "$CONTROL_SERVER_NAME")
fi

if [ -n "$BASIC_AUTH" ]; then
  ARGS+=(--basic-auth "$BASIC_AUTH")
fi

if [ "$STREAMER" = "true" ]; then
  ARGS+=(--streamer)
fi

bashio::log.info "Starting Ratatosk tunnel to ${SERVER}..."
exec /usr/local/bin/ratatosk "${ARGS[@]}"
