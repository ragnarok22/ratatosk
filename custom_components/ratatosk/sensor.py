"""Sensor platform for the Ratatosk integration."""

from __future__ import annotations

from datetime import datetime

from homeassistant.components.sensor import (
    SensorDeviceClass,
    SensorEntity,
    SensorStateClass,
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
    """Set up Ratatosk sensors."""
    coordinator: RatatoskDataUpdateCoordinator = entry.runtime_data.coordinator
    entry_id = entry.entry_id
    async_add_entities([
        RatatoskRequestCountSensor(coordinator, entry_id),
        RatatoskLastRequestSensor(coordinator, entry_id),
    ])


class RatatoskRequestCountSensor(RatatoskEntity, SensorEntity):
    """Sensor showing the number of proxied requests."""

    _attr_translation_key = "request_count"
    _attr_state_class = SensorStateClass.TOTAL_INCREASING
    _attr_icon = "mdi:counter"

    def __init__(
        self,
        coordinator: RatatoskDataUpdateCoordinator,
        entry_id: str,
    ) -> None:
        super().__init__(coordinator, entry_id)
        self._attr_unique_id = f"{entry_id}_request_count"

    @property
    def native_value(self) -> int | None:
        """Return the number of proxied requests."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.request_count


class RatatoskLastRequestSensor(RatatoskEntity, SensorEntity):
    """Sensor showing the timestamp of the last proxied request."""

    _attr_translation_key = "last_request"
    _attr_device_class = SensorDeviceClass.TIMESTAMP
    _attr_icon = "mdi:clock-outline"

    def __init__(
        self,
        coordinator: RatatoskDataUpdateCoordinator,
        entry_id: str,
    ) -> None:
        super().__init__(coordinator, entry_id)
        self._attr_unique_id = f"{entry_id}_last_request"

    @property
    def native_value(self) -> datetime | None:
        """Return the timestamp of the last request."""
        if self.coordinator.data is None:
            return None
        return self.coordinator.data.last_request
