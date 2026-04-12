"""Base entity for the Ratatosk integration."""

from __future__ import annotations

from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from .const import DOMAIN
from .coordinator import RatatoskDataUpdateCoordinator


class RatatoskEntity(CoordinatorEntity[RatatoskDataUpdateCoordinator]):
    """Base class for Ratatosk entities."""

    _attr_has_entity_name = True

    def __init__(
        self,
        coordinator: RatatoskDataUpdateCoordinator,
        entry_id: str,
    ) -> None:
        super().__init__(coordinator)
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, entry_id)},
            name="Ratatosk Tunnel",
            manufacturer="Ratatosk",
        )
