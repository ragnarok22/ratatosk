"""API client for the Ratatosk inspector."""

from __future__ import annotations

import asyncio
import socket
from typing import Any

import aiohttp


class RatatoskApiClientError(Exception):
    """Base exception for Ratatosk API errors."""


class RatatoskApiClientCommunicationError(RatatoskApiClientError):
    """Exception for communication errors."""


class RatatoskApiClient:
    """Client for the Ratatosk inspector HTTP API."""

    def __init__(
        self, host: str, port: int, session: aiohttp.ClientSession
    ) -> None:
        self._base_url = f"http://{host}:{port}"
        self._session = session

    async def async_get_logs(self) -> list[dict[str, Any]]:
        """Fetch traffic logs from the inspector API."""
        try:
            async with asyncio.timeout(10):
                response = await self._session.get(
                    f"{self._base_url}/api/logs",
                )
                response.raise_for_status()
                return await response.json()
        except TimeoutError as err:
            raise RatatoskApiClientCommunicationError(
                f"Timeout connecting to Ratatosk inspector at {self._base_url}"
            ) from err
        except (aiohttp.ClientError, socket.gaierror) as err:
            raise RatatoskApiClientCommunicationError(
                f"Cannot reach Ratatosk inspector at {self._base_url}: {err}"
            ) from err
