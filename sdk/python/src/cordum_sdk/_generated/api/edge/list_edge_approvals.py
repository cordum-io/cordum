from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.edge_approval_page_response import EdgeApprovalPageResponse
from ...models.list_edge_approvals_status import ListEdgeApprovalsStatus
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union


def _get_kwargs(
    *,
    status: Union[Unset, ListEdgeApprovalsStatus] = UNSET,
    session_id: Union[Unset, str] = UNSET,
    execution_id: Union[Unset, str] = UNSET,
    action_hash: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    params: Dict[str, Any] = {}

    json_status: Union[Unset, str] = UNSET
    if not isinstance(status, Unset):
        json_status = status.value

    params["status"] = json_status

    params["session_id"] = session_id

    params["execution_id"] = execution_id

    params["action_hash"] = action_hash

    params["cursor"] = cursor

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/edge/approvals",
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, EdgeApprovalPageResponse]]:
    if response.status_code == 200:
        response_200 = EdgeApprovalPageResponse.from_dict(response.json())

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
    if response.status_code == 500:
        response_500 = cast(Any, None)
        return response_500
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, EdgeApprovalPageResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListEdgeApprovalsStatus] = UNSET,
    session_id: Union[Unset, str] = UNSET,
    execution_id: Union[Unset, str] = UNSET,
    action_hash: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeApprovalPageResponse]]:
    """List Edge action approvals

     Lists tenant-scoped Edge approval records for action-level governance. Non-admin/non-operator
    callers receive only approvals whose `principal_id` matches the authenticated `auth.PrincipalID`;
    admin and operator callers may list all approvals in the tenant for operations and forensics.
    Status, tuple, cursor, and limit filters are applied within that visibility scope so list pagination
    remains principal-stable. Raw action payloads are never returned; actions are represented by
    `event_id`, `action_hash`, `input_hash`, and `policy_snapshot`.

    Args:
        status (Union[Unset, ListEdgeApprovalsStatus]):
        session_id (Union[Unset, str]):
        execution_id (Union[Unset, str]):
        action_hash (Union[Unset, str]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeApprovalPageResponse]]
    """

    kwargs = _get_kwargs(
        status=status,
        session_id=session_id,
        execution_id=execution_id,
        action_hash=action_hash,
        cursor=cursor,
        limit=limit,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListEdgeApprovalsStatus] = UNSET,
    session_id: Union[Unset, str] = UNSET,
    execution_id: Union[Unset, str] = UNSET,
    action_hash: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeApprovalPageResponse]]:
    """List Edge action approvals

     Lists tenant-scoped Edge approval records for action-level governance. Non-admin/non-operator
    callers receive only approvals whose `principal_id` matches the authenticated `auth.PrincipalID`;
    admin and operator callers may list all approvals in the tenant for operations and forensics.
    Status, tuple, cursor, and limit filters are applied within that visibility scope so list pagination
    remains principal-stable. Raw action payloads are never returned; actions are represented by
    `event_id`, `action_hash`, `input_hash`, and `policy_snapshot`.

    Args:
        status (Union[Unset, ListEdgeApprovalsStatus]):
        session_id (Union[Unset, str]):
        execution_id (Union[Unset, str]):
        action_hash (Union[Unset, str]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeApprovalPageResponse]
    """

    return sync_detailed(
        client=client,
        status=status,
        session_id=session_id,
        execution_id=execution_id,
        action_hash=action_hash,
        cursor=cursor,
        limit=limit,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListEdgeApprovalsStatus] = UNSET,
    session_id: Union[Unset, str] = UNSET,
    execution_id: Union[Unset, str] = UNSET,
    action_hash: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeApprovalPageResponse]]:
    """List Edge action approvals

     Lists tenant-scoped Edge approval records for action-level governance. Non-admin/non-operator
    callers receive only approvals whose `principal_id` matches the authenticated `auth.PrincipalID`;
    admin and operator callers may list all approvals in the tenant for operations and forensics.
    Status, tuple, cursor, and limit filters are applied within that visibility scope so list pagination
    remains principal-stable. Raw action payloads are never returned; actions are represented by
    `event_id`, `action_hash`, `input_hash`, and `policy_snapshot`.

    Args:
        status (Union[Unset, ListEdgeApprovalsStatus]):
        session_id (Union[Unset, str]):
        execution_id (Union[Unset, str]):
        action_hash (Union[Unset, str]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeApprovalPageResponse]]
    """

    kwargs = _get_kwargs(
        status=status,
        session_id=session_id,
        execution_id=execution_id,
        action_hash=action_hash,
        cursor=cursor,
        limit=limit,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListEdgeApprovalsStatus] = UNSET,
    session_id: Union[Unset, str] = UNSET,
    execution_id: Union[Unset, str] = UNSET,
    action_hash: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeApprovalPageResponse]]:
    """List Edge action approvals

     Lists tenant-scoped Edge approval records for action-level governance. Non-admin/non-operator
    callers receive only approvals whose `principal_id` matches the authenticated `auth.PrincipalID`;
    admin and operator callers may list all approvals in the tenant for operations and forensics.
    Status, tuple, cursor, and limit filters are applied within that visibility scope so list pagination
    remains principal-stable. Raw action payloads are never returned; actions are represented by
    `event_id`, `action_hash`, `input_hash`, and `policy_snapshot`.

    Args:
        status (Union[Unset, ListEdgeApprovalsStatus]):
        session_id (Union[Unset, str]):
        execution_id (Union[Unset, str]):
        action_hash (Union[Unset, str]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeApprovalPageResponse]
    """

    return (
        await asyncio_detailed(
            client=client,
            status=status,
            session_id=session_id,
            execution_id=execution_id,
            action_hash=action_hash,
            cursor=cursor,
            limit=limit,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
