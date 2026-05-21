from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.put_role_response_200 import PutRoleResponse200
from ...models.role_request import RoleRequest
from typing import cast
from typing import Dict


def _get_kwargs(
    name: str,
    *,
    body: RoleRequest,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "put",
        "url": "/api/v1/auth/roles/{name}".format(
            name=name,
        ),
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, PutRoleResponse200]]:
    if response.status_code == 200:
        response_200 = PutRoleResponse200.from_dict(response.json())

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
    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, PutRoleResponse200]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    name: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: RoleRequest,
    x_tenant_id: str,
) -> Response[Union[Any, PutRoleResponse200]]:
    """Create or update an RBAC role

    Args:
        name (str):
        x_tenant_id (str):
        body (RoleRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, PutRoleResponse200]]
    """

    kwargs = _get_kwargs(
        name=name,
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    name: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: RoleRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, PutRoleResponse200]]:
    """Create or update an RBAC role

    Args:
        name (str):
        x_tenant_id (str):
        body (RoleRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, PutRoleResponse200]
    """

    return sync_detailed(
        name=name,
        client=client,
        body=body,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    name: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: RoleRequest,
    x_tenant_id: str,
) -> Response[Union[Any, PutRoleResponse200]]:
    """Create or update an RBAC role

    Args:
        name (str):
        x_tenant_id (str):
        body (RoleRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, PutRoleResponse200]]
    """

    kwargs = _get_kwargs(
        name=name,
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    name: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: RoleRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, PutRoleResponse200]]:
    """Create or update an RBAC role

    Args:
        name (str):
        x_tenant_id (str):
        body (RoleRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, PutRoleResponse200]
    """

    return (
        await asyncio_detailed(
            name=name,
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
