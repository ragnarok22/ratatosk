"""Binary sensor platform for the Ratatosk integration."""

from __future__ import annotations

from homeassistant.components.binary_sensor import (
    BinarySensorDeviceClass,
    BinarySensorEntity,
)
from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .coordinator import RatatoskDataUpdateCoordinator
from .entity import RatatoskEntity


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    """Set up Ratatosk binary sensors."""
    coordinator: RatatoskDataUpdateCoordinator = entry.runtime_data.coordinator
    async_add_entities([RatatoskConnectedSensor(coordinator, entry.entry_id)])


class RatatoskConnectedSensor(RatatoskEntity, BinarySensorEntity):
    """Binary sensor indicating whether the tunnel is connected."""

    _attr_device_class = BinarySensorDeviceClass.CONNECTIVITY
    _attr_translation_key = "connected"

    def __init__(
        self,
        coordinator: RatatoskDataUpdateCoordinator,
        entry_id: str,
    ) -> None:
        super().__init__(coordinator, entry_id)
        self._attr_unique_id = f"{entry_id}_connected"

    @property
    def is_on(self) -> bool | None:
        """Return True if the tunnel is connected."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.connected
