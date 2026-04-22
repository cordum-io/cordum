from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.get_agent_denied_events_response_200 import GetAgentDeniedEventsResponse200
from typing import cast
from typing import Dict



def _get_kwargs(
    id: str,
    *,
    x_tenant_id: str,

) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id



    

    

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/agents/{id}/denied-events".format(id=id,),
    }


    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Optional[Union[Any, GetAgentDeniedEventsResponse200]]:
    if response.status_code == 200:
        response_200 = GetAgentDeniedEventsResponse200.from_dict(response.json())



        return response_200
    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401
    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Response[Union[Any, GetAgentDeniedEventsResponse200]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient,
    x_tenant_id: str,

) -> Response[Union[Any, GetAgentDeniedEventsResponse200]]:
    r""" Recent mcp_tool_denied events for an agent identity

     Returns up to 50 of the most recent `mcp_tool_denied` audit events for the identity from the
    gateway's in-memory ring. Feeds the dashboard \"recent denials\" panel without requiring a SIEM
    pipeline.

    Args:
        id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, GetAgentDeniedEventsResponse200]]
     """


    kwargs = _get_kwargs(
        id=id,
x_tenant_id=x_tenant_id,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    id: str,
    *,
    client: AuthenticatedClient,
    x_tenant_id: str,

) -> Optional[Union[Any, GetAgentDeniedEventsResponse200]]:
    r""" Recent mcp_tool_denied events for an agent identity

     Returns up to 50 of the most recent `mcp_tool_denied` audit events for the identity from the
    gateway's in-memory ring. Feeds the dashboard \"recent denials\" panel without requiring a SIEM
    pipeline.

    Args:
        id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, GetAgentDeniedEventsResponse200]
     """


    return sync_detailed(
        id=id,
client=client,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient,
    x_tenant_id: str,

) -> Response[Union[Any, GetAgentDeniedEventsResponse200]]:
    r""" Recent mcp_tool_denied events for an agent identity

     Returns up to 50 of the most recent `mcp_tool_denied` audit events for the identity from the
    gateway's in-memory ring. Feeds the dashboard \"recent denials\" panel without requiring a SIEM
    pipeline.

    Args:
        id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, GetAgentDeniedEventsResponse200]]
     """


    kwargs = _get_kwargs(
        id=id,
x_tenant_id=x_tenant_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient,
    x_tenant_id: str,

) -> Optional[Union[Any, GetAgentDeniedEventsResponse200]]:
    r""" Recent mcp_tool_denied events for an agent identity

     Returns up to 50 of the most recent `mcp_tool_denied` audit events for the identity from the
    gateway's in-memory ring. Feeds the dashboard \"recent denials\" panel without requiring a SIEM
    pipeline.

    Args:
        id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, GetAgentDeniedEventsResponse200]
     """


    return (await asyncio_detailed(
        id=id,
client=client,
x_tenant_id=x_tenant_id,

    )).parsed
