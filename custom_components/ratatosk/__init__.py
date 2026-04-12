"""The Ratatosk integration."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import timedelta

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import Platform
from homeassistant.core import HomeAssistant
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .api import RatatoskApiClient
from .const import DOMAIN, LOGGER, SCAN_INTERVAL_SECONDS
from .coordinator import RatatoskDataUpdateCoordinator

PLATFORMS: list[Platform] = [Platform.BINARY_SENSOR, Platform.SENSOR]


@dataclass
class RatatoskData:
    """Runtime data for a Ratatosk config entry."""

    client: RatatoskApiClient
    coordinator: RatatoskDataUpdateCoordinator


async def async_setup_entry(hass: HomeAssistant, entry: ConfigEntry) -> bool:
    """Set up Ratatosk from a config entry."""
    client = RatatoskApiClient(
        host=entry.data["host"],
        port=int(entry.data["port"]),
        session=async_get_clientsession(hass),
    )
    coordinator = RatatoskDataUpdateCoordinator(
        hass,
        LOGGER,
        name=DOMAIN,
        config_entry=entry,
        update_interval=timedelta(seconds=SCAN_INTERVAL_SECONDS),
        client=client,
    )

    await coordinator.async_config_entry_first_refresh()

    entry.runtime_data = RatatoskData(client=client, coordinator=coordinator)

    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    return True


async def async_unload_entry(hass: HomeAssistant, entry: ConfigEntry) -> bool:
    """Unload a Ratatosk config entry."""
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
