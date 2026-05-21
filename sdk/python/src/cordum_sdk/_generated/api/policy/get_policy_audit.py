from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.policy_audit_envelope import PolicyAuditEnvelope
from ...types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Dict
from typing import Union
import datetime


def _get_kwargs(
    *,
    limit: Union[Unset, int] = 100,
    offset: Union[Unset, int] = 0,
    action: Union[Unset, str] = UNSET,
    agent_id: Union[Unset, str] = UNSET,
    after: Union[Unset, datetime.datetime] = UNSET,
    before: Union[Unset, datetime.datetime] = UNSET,
    search: Union[Unset, str] = UNSET,
    rule_id: Union[Unset, str] = UNSET,
    type: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    params: Dict[str, Any] = {}

    params["limit"] = limit

    params["offset"] = offset

    params["action"] = action

    params["agent_id"] = agent_id

    json_after: Union[Unset, str] = UNSET
    if not isinstance(after, Unset):
        json_after = after.isoformat()
    params["after"] = json_after

    json_before: Union[Unset, str] = UNSET
    if not isinstance(before, Unset):
        json_before = before.isoformat()
    params["before"] = json_before

    params["search"] = search

    params["rule_id"] = rule_id

    params["type"] = type

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/policy/audit",
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, PolicyAuditEnvelope]]:
    if response.status_code == 200:
        response_200 = PolicyAuditEnvelope.from_dict(response.json())

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
) -> Response[Union[Any, PolicyAuditEnvelope]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    limit: Union[Unset, int] = 100,
    offset: Union[Unset, int] = 0,
    action: Union[Unset, str] = UNSET,
    agent_id: Union[Unset, str] = UNSET,
    after: Union[Unset, datetime.datetime] = UNSET,
    before: Union[Unset, datetime.datetime] = UNSET,
    search: Union[Unset, str] = UNSET,
    rule_id: Union[Unset, str] = UNSET,
    type: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, PolicyAuditEnvelope]]:
    """Get policy audit log

     Returns a filtered, paginated list of policy audit events. Filters
    match the gateway handler `handleListPolicyAudit` semantics: `action`
    / `agent_id` / `rule_id` / `type` are case-insensitive exact matches;
    `after` / `before` are lexicographic compares against `created_at`;
    `search` is a substring match across `action + actor_id +
    resource_type + resource_id + message` lowercased.

    Args:
        limit (Union[Unset, int]):  Default: 100.
        offset (Union[Unset, int]):  Default: 0.
        action (Union[Unset, str]):
        agent_id (Union[Unset, str]):
        after (Union[Unset, datetime.datetime]):
        before (Union[Unset, datetime.datetime]):
        search (Union[Unset, str]):
        rule_id (Union[Unset, str]):
        type (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, PolicyAuditEnvelope]]
    """

    kwargs = _get_kwargs(
        limit=limit,
        offset=offset,
        action=action,
        agent_id=agent_id,
        after=after,
        before=before,
        search=search,
        rule_id=rule_id,
        type=type,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    limit: Union[Unset, int] = 100,
    offset: Union[Unset, int] = 0,
    action: Union[Unset, str] = UNSET,
    agent_id: Union[Unset, str] = UNSET,
    after: Union[Unset, datetime.datetime] = UNSET,
    before: Union[Unset, datetime.datetime] = UNSET,
    search: Union[Unset, str] = UNSET,
    rule_id: Union[Unset, str] = UNSET,
    type: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, PolicyAuditEnvelope]]:
    """Get policy audit log

     Returns a filtered, paginated list of policy audit events. Filters
    match the gateway handler `handleListPolicyAudit` semantics: `action`
    / `agent_id` / `rule_id` / `type` are case-insensitive exact matches;
    `after` / `before` are lexicographic compares against `created_at`;
    `search` is a substring match across `action + actor_id +
    resource_type + resource_id + message` lowercased.

    Args:
        limit (Union[Unset, int]):  Default: 100.
        offset (Union[Unset, int]):  Default: 0.
        action (Union[Unset, str]):
        agent_id (Union[Unset, str]):
        after (Union[Unset, datetime.datetime]):
        before (Union[Unset, datetime.datetime]):
        search (Union[Unset, str]):
        rule_id (Union[Unset, str]):
        type (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, PolicyAuditEnvelope]
    """

    return sync_detailed(
        client=client,
        limit=limit,
        offset=offset,
        action=action,
        agent_id=agent_id,
        after=after,
        before=before,
        search=search,
        rule_id=rule_id,
        type=type,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    limit: Union[Unset, int] = 100,
    offset: Union[Unset, int] = 0,
    action: Union[Unset, str] = UNSET,
    agent_id: Union[Unset, str] = UNSET,
    after: Union[Unset, datetime.datetime] = UNSET,
    before: Union[Unset, datetime.datetime] = UNSET,
    search: Union[Unset, str] = UNSET,
    rule_id: Union[Unset, str] = UNSET,
    type: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, PolicyAuditEnvelope]]:
    """Get policy audit log

     Returns a filtered, paginated list of policy audit events. Filters
    match the gateway handler `handleListPolicyAudit` semantics: `action`
    / `agent_id` / `rule_id` / `type` are case-insensitive exact matches;
    `after` / `before` are lexicographic compares against `created_at`;
    `search` is a substring match across `action + actor_id +
    resource_type + resource_id + message` lowercased.

    Args:
        limit (Union[Unset, int]):  Default: 100.
        offset (Union[Unset, int]):  Default: 0.
        action (Union[Unset, str]):
        agent_id (Union[Unset, str]):
        after (Union[Unset, datetime.datetime]):
        before (Union[Unset, datetime.datetime]):
        search (Union[Unset, str]):
        rule_id (Union[Unset, str]):
        type (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, PolicyAuditEnvelope]]
    """

    kwargs = _get_kwargs(
        limit=limit,
        offset=offset,
        action=action,
        agent_id=agent_id,
        after=after,
        before=before,
        search=search,
        rule_id=rule_id,
        type=type,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    limit: Union[Unset, int] = 100,
    offset: Union[Unset, int] = 0,
    action: Union[Unset, str] = UNSET,
    agent_id: Union[Unset, str] = UNSET,
    after: Union[Unset, datetime.datetime] = UNSET,
    before: Union[Unset, datetime.datetime] = UNSET,
    search: Union[Unset, str] = UNSET,
    rule_id: Union[Unset, str] = UNSET,
    type: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, PolicyAuditEnvelope]]:
    """Get policy audit log

     Returns a filtered, paginated list of policy audit events. Filters
    match the gateway handler `handleListPolicyAudit` semantics: `action`
    / `agent_id` / `rule_id` / `type` are case-insensitive exact matches;
    `after` / `before` are lexicographic compares against `created_at`;
    `search` is a substring match across `action + actor_id +
    resource_type + resource_id + message` lowercased.

    Args:
        limit (Union[Unset, int]):  Default: 100.
        offset (Union[Unset, int]):  Default: 0.
        action (Union[Unset, str]):
        agent_id (Union[Unset, str]):
        after (Union[Unset, datetime.datetime]):
        before (Union[Unset, datetime.datetime]):
        search (Union[Unset, str]):
        rule_id (Union[Unset, str]):
        type (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, PolicyAuditEnvelope]
    """

    return (
        await asyncio_detailed(
            client=client,
            limit=limit,
            offset=offset,
            action=action,
            agent_id=agent_id,
            after=after,
            before=before,
            search=search,
            rule_id=rule_id,
            type=type,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
