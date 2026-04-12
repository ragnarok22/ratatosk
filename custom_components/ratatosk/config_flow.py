"""Config flow for the Ratatosk integration."""

from __future__ import annotations

import voluptuous as vol
from homeassistant import config_entries
from homeassistant.helpers import selector
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .api import RatatoskApiClient, RatatoskApiClientCommunicationError, RatatoskApiClientError
from .const import DEFAULT_HOST, DEFAULT_PORT, DOMAIN


class RatatoskConfigFlow(config_entries.ConfigFlow, domain=DOMAIN):
    """Handle a config flow for Ratatosk."""

    VERSION = 1

    async def async_step_user(
        self,
        user_input: dict | None = None,
    ) -> config_entries.ConfigFlowResult:
        """Handle the initial setup step."""
        errors: dict[str, str] = {}

        if user_input is not None:
            host = user_input["host"]
            port = int(user_input["port"])
            try:
                client = RatatoskApiClient(
                    host=host,
                    port=port,
                    session=async_get_clientsession(self.hass),
                )
                await client.async_get_logs()
            except RatatoskApiClientCommunicationError:
                errors["base"] = "cannot_connect"
            except RatatoskApiClientError:
                errors["base"] = "unknown"
            else:
                await self.async_set_unique_id(f"{host}:{port}")
                self._abort_if_unique_id_configured()
                return self.async_create_entry(
                    title=f"Ratatosk ({host}:{port})",
                    data={"host": host, "port": port},
                )

        return self.async_show_form(
            step_id="user",
            data_schema=vol.Schema(
                {
                    vol.Required("host", default=DEFAULT_HOST): selector.TextSelector(
                        selector.TextSelectorConfig(
                            type=selector.TextSelectorType.TEXT
                        ),
                    ),
                    vol.Required("port", default=DEFAULT_PORT): selector.NumberSelector(
                        selector.NumberSelectorConfig(
                            min=1,
                            max=65535,
                            mode=selector.NumberSelectorMode.BOX,
                        ),
                    ),
                }
            ),
            errors=errors,
        )
