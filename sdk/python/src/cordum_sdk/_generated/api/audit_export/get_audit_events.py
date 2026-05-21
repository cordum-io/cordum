from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.audit_events_envelope import AuditEventsEnvelope
from ...types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Dict
from typing import Union
import datetime


def _get_kwargs(
    *,
    tenant: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    event_type: Union[Unset, str] = UNSET,
    severity: Union[Unset, str] = UNSET,
    from_: Union[Unset, datetime.datetime] = UNSET,
    to: Union[Unset, datetime.datetime] = UNSET,
    search: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    params: Dict[str, Any] = {}

    params["tenant"] = tenant

    params["limit"] = limit

    params["cursor"] = cursor

    params["event_type"] = event_type

    params["severity"] = severity

    json_from_: Union[Unset, str] = UNSET
    if not isinstance(from_, Unset):
        json_from_ = from_.isoformat()
    params["from"] = json_from_

    json_to: Union[Unset, str] = UNSET
    if not isinstance(to, Unset):
        json_to = to.isoformat()
    params["to"] = json_to

    params["search"] = search

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/audit/events",
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, AuditEventsEnvelope]]:
    if response.status_code == 200:
        response_200 = AuditEventsEnvelope.from_dict(response.json())

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
    if response.status_code == 500:
        response_500 = cast(Any, None)
        return response_500
    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, AuditEventsEnvelope]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    tenant: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    event_type: Union[Unset, str] = UNSET,
    severity: Union[Unset, str] = UNSET,
    from_: Union[Unset, datetime.datetime] = UNSET,
    to: Union[Unset, datetime.datetime] = UNSET,
    search: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, AuditEventsEnvelope]]:
    """List audit events from the SIEM feed

     Returns a paginated list of SIEM audit events for a tenant — the full
    chained feed (MCP, edge, worker, output policy, delegation, license, ...)
    sourced from the per-tenant Redis Stream populated by the audit chainer.
    Distinct from `/api/v1/policy/audit`, which only surfaces the
    policy-bundle audit subset.

    Reverse-chronological by chain sequence. Filters (event_type / severity /
    from / to / search) apply in-process after the stream read. Defense-in-depth
    redaction strips Extra-map keys matching the secret-key pattern
    (token / password / api_key / private_key / secret) before serialization.

    Every successful read appends an `audit.read.events` event to the same
    chain so the read surface is itself auditable.

    Permission: `audit.read` (admin/operator/viewer with RBAC; admin via
    legacy-role fallback). 503 when the audit chainer is not configured.

    Args:
        tenant (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 100.
        cursor (Union[Unset, str]):
        event_type (Union[Unset, str]):
        severity (Union[Unset, str]):
        from_ (Union[Unset, datetime.datetime]):
        to (Union[Unset, datetime.datetime]):
        search (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, AuditEventsEnvelope]]
    """

    kwargs = _get_kwargs(
        tenant=tenant,
        limit=limit,
        cursor=cursor,
        event_type=event_type,
        severity=severity,
        from_=from_,
        to=to,
        search=search,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    tenant: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    event_type: Union[Unset, str] = UNSET,
    severity: Union[Unset, str] = UNSET,
    from_: Union[Unset, datetime.datetime] = UNSET,
    to: Union[Unset, datetime.datetime] = UNSET,
    search: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, AuditEventsEnvelope]]:
    """List audit events from the SIEM feed

     Returns a paginated list of SIEM audit events for a tenant — the full
    chained feed (MCP, edge, worker, output policy, delegation, license, ...)
    sourced from the per-tenant Redis Stream populated by the audit chainer.
    Distinct from `/api/v1/policy/audit`, which only surfaces the
    policy-bundle audit subset.

    Reverse-chronological by chain sequence. Filters (event_type / severity /
    from / to / search) apply in-process after the stream read. Defense-in-depth
    redaction strips Extra-map keys matching the secret-key pattern
    (token / password / api_key / private_key / secret) before serialization.

    Every successful read appends an `audit.read.events` event to the same
    chain so the read surface is itself auditable.

    Permission: `audit.read` (admin/operator/viewer with RBAC; admin via
    legacy-role fallback). 503 when the audit chainer is not configured.

    Args:
        tenant (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 100.
        cursor (Union[Unset, str]):
        event_type (Union[Unset, str]):
        severity (Union[Unset, str]):
        from_ (Union[Unset, datetime.datetime]):
        to (Union[Unset, datetime.datetime]):
        search (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, AuditEventsEnvelope]
    """

    return sync_detailed(
        client=client,
        tenant=tenant,
        limit=limit,
        cursor=cursor,
        event_type=event_type,
        severity=severity,
        from_=from_,
        to=to,
        search=search,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    tenant: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    event_type: Union[Unset, str] = UNSET,
    severity: Union[Unset, str] = UNSET,
    from_: Union[Unset, datetime.datetime] = UNSET,
    to: Union[Unset, datetime.datetime] = UNSET,
    search: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, AuditEventsEnvelope]]:
    """List audit events from the SIEM feed

     Returns a paginated list of SIEM audit events for a tenant — the full
    chained feed (MCP, edge, worker, output policy, delegation, license, ...)
    sourced from the per-tenant Redis Stream populated by the audit chainer.
    Distinct from `/api/v1/policy/audit`, which only surfaces the
    policy-bundle audit subset.

    Reverse-chronological by chain sequence. Filters (event_type / severity /
    from / to / search) apply in-process after the stream read. Defense-in-depth
    redaction strips Extra-map keys matching the secret-key pattern
    (token / password / api_key / private_key / secret) before serialization.

    Every successful read appends an `audit.read.events` event to the same
    chain so the read surface is itself auditable.

    Permission: `audit.read` (admin/operator/viewer with RBAC; admin via
    legacy-role fallback). 503 when the audit chainer is not configured.

    Args:
        tenant (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 100.
        cursor (Union[Unset, str]):
        event_type (Union[Unset, str]):
        severity (Union[Unset, str]):
        from_ (Union[Unset, datetime.datetime]):
        to (Union[Unset, datetime.datetime]):
        search (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, AuditEventsEnvelope]]
    """

    kwargs = _get_kwargs(
        tenant=tenant,
        limit=limit,
        cursor=cursor,
        event_type=event_type,
        severity=severity,
        from_=from_,
        to=to,
        search=search,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    tenant: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    event_type: Union[Unset, str] = UNSET,
    severity: Union[Unset, str] = UNSET,
    from_: Union[Unset, datetime.datetime] = UNSET,
    to: Union[Unset, datetime.datetime] = UNSET,
    search: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, AuditEventsEnvelope]]:
    """List audit events from the SIEM feed

     Returns a paginated list of SIEM audit events for a tenant — the full
    chained feed (MCP, edge, worker, output policy, delegation, license, ...)
    sourced from the per-tenant Redis Stream populated by the audit chainer.
    Distinct from `/api/v1/policy/audit`, which only surfaces the
    policy-bundle audit subset.

    Reverse-chronological by chain sequence. Filters (event_type / severity /
    from / to / search) apply in-process after the stream read. Defense-in-depth
    redaction strips Extra-map keys matching the secret-key pattern
    (token / password / api_key / private_key / secret) before serialization.

    Every successful read appends an `audit.read.events` event to the same
    chain so the read surface is itself auditable.

    Permission: `audit.read` (admin/operator/viewer with RBAC; admin via
    legacy-role fallback). 503 when the audit chainer is not configured.

    Args:
        tenant (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 100.
        cursor (Union[Unset, str]):
        event_type (Union[Unset, str]):
        severity (Union[Unset, str]):
        from_ (Union[Unset, datetime.datetime]):
        to (Union[Unset, datetime.datetime]):
        search (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, AuditEventsEnvelope]
    """

    return (
        await asyncio_detailed(
            client=client,
            tenant=tenant,
            limit=limit,
            cursor=cursor,
            event_type=event_type,
            severity=severity,
            from_=from_,
            to=to,
            search=search,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
