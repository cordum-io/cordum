from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.edge_end_session_request import EdgeEndSessionRequest
from ...models.edge_session import EdgeSession
from typing import cast
from typing import Dict


def _get_kwargs(
    session_id: str,
    *,
    body: EdgeEndSessionRequest,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/edge/sessions/{session_id}/end".format(
            session_id=session_id,
        ),
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, EdgeSession]]:
    if response.status_code == 200:
        response_200 = EdgeSession.from_dict(response.json())

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
    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503
    if response.status_code == 500:
        response_500 = cast(Any, None)
        return response_500
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, EdgeSession]]:
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
    body: EdgeEndSessionRequest,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeSession]]:
    """End an Edge session

    Args:
        session_id (str):
        x_tenant_id (str):
        body (EdgeEndSessionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeSession]]
    """

    kwargs = _get_kwargs(
        session_id=session_id,
        body=body,
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
    body: EdgeEndSessionRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeSession]]:
    """End an Edge session

    Args:
        session_id (str):
        x_tenant_id (str):
        body (EdgeEndSessionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeSession]
    """

    return sync_detailed(
        session_id=session_id,
        client=client,
        body=body,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    session_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeEndSessionRequest,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeSession]]:
    """End an Edge session

    Args:
        session_id (str):
        x_tenant_id (str):
        body (EdgeEndSessionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeSession]]
    """

    kwargs = _get_kwargs(
        session_id=session_id,
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    session_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeEndSessionRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeSession]]:
    """End an Edge session

    Args:
        session_id (str):
        x_tenant_id (str):
        body (EdgeEndSessionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeSession]
    """

    return (
        await asyncio_detailed(
            session_id=session_id,
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
