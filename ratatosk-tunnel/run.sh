#!/usr/bin/with-contenv bashio

SERVER=$(bashio::config 'server')
PORT=$(bashio::config 'port')
BASIC_AUTH=$(bashio::config 'basic_auth')
STREAMER=$(bashio::config 'streamer')

if [ -z "$SERVER" ]; then
  bashio::log.fatal "The 'server' option is required. Set it to your Ratatosk relay server address (e.g., tunnel.example.com:7000)."
  exit 1
fi

ARGS="--server ${SERVER} --port ${PORT}"

if [ -n "$BASIC_AUTH" ]; then
  ARGS="${ARGS} --basic-auth ${BASIC_AUTH}"
fi

if [ "$STREAMER" = "true" ]; then
  ARGS="${ARGS} --streamer"
fi

bashio::log.info "Starting Ratatosk tunnel to ${SERVER}..."
exec /usr/local/bin/ratatosk ${ARGS}
