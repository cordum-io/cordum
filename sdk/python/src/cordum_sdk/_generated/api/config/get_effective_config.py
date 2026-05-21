from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.config_document import ConfigDocument
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union


def _get_kwargs(
    *,
    org_id: Union[Unset, str] = UNSET,
    team_id: Union[Unset, str] = UNSET,
    workflow_id: Union[Unset, str] = UNSET,
    step_id: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    params: Dict[str, Any] = {}

    params["org_id"] = org_id

    params["team_id"] = team_id

    params["workflow_id"] = workflow_id

    params["step_id"] = step_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/config/effective",
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, ConfigDocument]]:
    if response.status_code == 200:
        response_200 = ConfigDocument.from_dict(response.json())

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
) -> Response[Union[Any, ConfigDocument]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    org_id: Union[Unset, str] = UNSET,
    team_id: Union[Unset, str] = UNSET,
    workflow_id: Union[Unset, str] = UNSET,
    step_id: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, ConfigDocument]]:
    """Get merged effective configuration

     Returns the effective configuration after merging all scopes
    (global -> org -> team -> workflow -> step).

    Args:
        org_id (Union[Unset, str]):
        team_id (Union[Unset, str]):
        workflow_id (Union[Unset, str]):
        step_id (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ConfigDocument]]
    """

    kwargs = _get_kwargs(
        org_id=org_id,
        team_id=team_id,
        workflow_id=workflow_id,
        step_id=step_id,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    org_id: Union[Unset, str] = UNSET,
    team_id: Union[Unset, str] = UNSET,
    workflow_id: Union[Unset, str] = UNSET,
    step_id: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, ConfigDocument]]:
    """Get merged effective configuration

     Returns the effective configuration after merging all scopes
    (global -> org -> team -> workflow -> step).

    Args:
        org_id (Union[Unset, str]):
        team_id (Union[Unset, str]):
        workflow_id (Union[Unset, str]):
        step_id (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ConfigDocument]
    """

    return sync_detailed(
        client=client,
        org_id=org_id,
        team_id=team_id,
        workflow_id=workflow_id,
        step_id=step_id,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    org_id: Union[Unset, str] = UNSET,
    team_id: Union[Unset, str] = UNSET,
    workflow_id: Union[Unset, str] = UNSET,
    step_id: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, ConfigDocument]]:
    """Get merged effective configuration

     Returns the effective configuration after merging all scopes
    (global -> org -> team -> workflow -> step).

    Args:
        org_id (Union[Unset, str]):
        team_id (Union[Unset, str]):
        workflow_id (Union[Unset, str]):
        step_id (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ConfigDocument]]
    """

    kwargs = _get_kwargs(
        org_id=org_id,
        team_id=team_id,
        workflow_id=workflow_id,
        step_id=step_id,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    org_id: Union[Unset, str] = UNSET,
    team_id: Union[Unset, str] = UNSET,
    workflow_id: Union[Unset, str] = UNSET,
    step_id: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, ConfigDocument]]:
    """Get merged effective configuration

     Returns the effective configuration after merging all scopes
    (global -> org -> team -> workflow -> step).

    Args:
        org_id (Union[Unset, str]):
        team_id (Union[Unset, str]):
        workflow_id (Union[Unset, str]):
        step_id (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ConfigDocument]
    """

    return (
        await asyncio_detailed(
            client=client,
            org_id=org_id,
            team_id=team_id,
            workflow_id=workflow_id,
            step_id=step_id,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
