from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.copilot_session_detail_response import CopilotSessionDetailResponse
from ...models.error import Error
from typing import cast
from typing import Dict


def _get_kwargs(
    session_id: str,
    *,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/copilot/sessions/{session_id}".format(
            session_id=session_id,
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, CopilotSessionDetailResponse, Error]]:
    if response.status_code == 200:
        response_200 = CopilotSessionDetailResponse.from_dict(response.json())

        return response_200
    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400
    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401
    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403
    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404
    if response.status_code == 501:
        response_501 = Error.from_dict(response.json())

        return response_501
    if response.status_code == 500:
        response_500 = cast(Any, None)
        return response_500
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, CopilotSessionDetailResponse, Error]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    session_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Response[Union[Any, CopilotSessionDetailResponse, Error]]:
    """Get Copilot session detail

     Returns a Copilot chat transcript plus linked jobs and governance decisions.

    Args:
        session_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, CopilotSessionDetailResponse, Error]]
    """

    kwargs = _get_kwargs(
        session_id=session_id,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    session_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Optional[Union[Any, CopilotSessionDetailResponse, Error]]:
    """Get Copilot session detail

     Returns a Copilot chat transcript plus linked jobs and governance decisions.

    Args:
        session_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, CopilotSessionDetailResponse, Error]
    """

    return sync_detailed(
        session_id=session_id,
        client=client,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    session_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Response[Union[Any, CopilotSessionDetailResponse, Error]]:
    """Get Copilot session detail

     Returns a Copilot chat transcript plus linked jobs and governance decisions.

    Args:
        session_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, CopilotSessionDetailResponse, Error]]
    """

    kwargs = _get_kwargs(
        session_id=session_id,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    session_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Optional[Union[Any, CopilotSessionDetailResponse, Error]]:
    """Get Copilot session detail

     Returns a Copilot chat transcript plus linked jobs and governance decisions.

    Args:
        session_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, CopilotSessionDetailResponse, Error]
    """

    return (
        await asyncio_detailed(
            session_id=session_id,
            client=client,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
