from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.list_policy_rules_response_200 import ListPolicyRulesResponse200
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union


def _get_kwargs(
    *,
    include_disabled: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    params: Dict[str, Any] = {}

    params["include_disabled"] = include_disabled

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/policy/rules",
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, ListPolicyRulesResponse200]]:
    if response.status_code == 200:
        response_200 = ListPolicyRulesResponse200.from_dict(response.json())

        return response_200
    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401
    if response.status_code == 500:
        response_500 = cast(Any, None)
        return response_500
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, ListPolicyRulesResponse200]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    include_disabled: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Response[Union[Any, ListPolicyRulesResponse200]]:
    """List policy rules

    Args:
        include_disabled (Union[Unset, bool]):  Default: False.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ListPolicyRulesResponse200]]
    """

    kwargs = _get_kwargs(
        include_disabled=include_disabled,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    include_disabled: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Optional[Union[Any, ListPolicyRulesResponse200]]:
    """List policy rules

    Args:
        include_disabled (Union[Unset, bool]):  Default: False.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ListPolicyRulesResponse200]
    """

    return sync_detailed(
        client=client,
        include_disabled=include_disabled,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    include_disabled: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Response[Union[Any, ListPolicyRulesResponse200]]:
    """List policy rules

    Args:
        include_disabled (Union[Unset, bool]):  Default: False.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ListPolicyRulesResponse200]]
    """

    kwargs = _get_kwargs(
        include_disabled=include_disabled,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    include_disabled: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Optional[Union[Any, ListPolicyRulesResponse200]]:
    """List policy rules

    Args:
        include_disabled (Union[Unset, bool]):  Default: False.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ListPolicyRulesResponse200]
    """

    return (
        await asyncio_detailed(
            client=client,
            include_disabled=include_disabled,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
