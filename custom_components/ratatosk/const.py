"""Constants for the Ratatosk integration."""

from logging import Logger, getLogger

LOGGER: Logger = getLogger(__package__)

DOMAIN = "ratatosk"
DEFAULT_HOST = "127.0.0.1"
DEFAULT_PORT = 4040
SCAN_INTERVAL_SECONDS = 30
