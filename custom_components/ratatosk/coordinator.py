"""DataUpdateCoordinator for the Ratatosk integration."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime

from homeassistant.helpers.update_coordinator import DataUpdateCoordinator
from homeassistant.util.dt import parse_datetime

from .api import RatatoskApiClient, RatatoskApiClientError
from .const import LOGGER


@dataclass
class RatatoskStatus:
    """Processed status from the Ratatosk inspector."""

    connected: bool
    request_count: int
    last_request: datetime | None


class RatatoskDataUpdateCoordinator(DataUpdateCoordinator[RatatoskStatus]):
    """Fetch and process data from the Ratatosk inspector API."""

    def __init__(self, *args, client: RatatoskApiClient, **kwargs) -> None:
        super().__init__(*args, **kwargs)
        self.client = client

    async def _async_update_data(self) -> RatatoskStatus:
        try:
            logs = await self.client.async_get_logs()
        except RatatoskApiClientError:
            return RatatoskStatus(
                connected=False, request_count=0, last_request=None
            )

        last_request = None
        if logs:
            ts = logs[-1].get("timestamp")
            if ts:
                last_request = parse_datetime(ts)

        return RatatoskStatus(
            connected=True,
            request_count=len(logs),
            last_request=last_request,
        )
