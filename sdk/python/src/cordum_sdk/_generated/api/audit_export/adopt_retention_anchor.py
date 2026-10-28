from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.adopt_retention_anchor_request import AdoptRetentionAnchorRequest
from ...models.adopt_retention_anchor_result import AdoptRetentionAnchorResult
from typing import cast
from typing import Dict


def _get_kwargs(
    *,
    body: AdoptRetentionAnchorRequest,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/audit/chain/retention-anchor",
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[AdoptRetentionAnchorResult, Any]]:
    if response.status_code == 200:
        response_200 = AdoptRetentionAnchorResult.from_dict(response.json())

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
    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409
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
) -> Response[Union[AdoptRetentionAnchorResult, Any]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: AdoptRetentionAnchorRequest,
    x_tenant_id: str,
) -> Response[Union[AdoptRetentionAnchorResult, Any]]:
    """Adopt a retention anchor for a chain trimmed outside the sweeper

     Writes the retention anchor that a tenant chain trimmed outside the retention sweeper (by
    hand, or by stream max-length eviction) lacks, so verify reports the missing prefix as
    `retention_trimmed` instead of `missing`. The anchor is the oldest retained event's
    predecessor: seq `expected_oldest_seq - 1`, the oldest event's `prev_hash`, and the stream
    ID just below its entry, HMAC-signed when `CORDUM_AUDIT_HMAC_KEY` is set. Admin-only.

    Refused with 409, changing nothing, when the oldest retained event is not
    `expected_oldest_seq`, when the current anchor already links the chain, when a Redis
    Cluster retention trim is in progress, or when the current anchor is invalid and
    `replace_invalid` is not set. The adoption is appended to the tenant's audit chain
    (`audit.retention_anchor_adopted`, naming the actor and the replaced anchor) before the
    anchor is written. If a retention sweep moves the anchor in between, the call returns
    409 and that event stays in the chain.

    Args:
        x_tenant_id (str):
        body (AdoptRetentionAnchorRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[AdoptRetentionAnchorResult, Any]]
    """

    kwargs = _get_kwargs(
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    body: AdoptRetentionAnchorRequest,
    x_tenant_id: str,
) -> Optional[Union[AdoptRetentionAnchorResult, Any]]:
    """Adopt a retention anchor for a chain trimmed outside the sweeper

     Writes the retention anchor that a tenant chain trimmed outside the retention sweeper (by
    hand, or by stream max-length eviction) lacks, so verify reports the missing prefix as
    `retention_trimmed` instead of `missing`. The anchor is the oldest retained event's
    predecessor: seq `expected_oldest_seq - 1`, the oldest event's `prev_hash`, and the stream
    ID just below its entry, HMAC-signed when `CORDUM_AUDIT_HMAC_KEY` is set. Admin-only.

    Refused with 409, changing nothing, when the oldest retained event is not
    `expected_oldest_seq`, when the current anchor already links the chain, when a Redis
    Cluster retention trim is in progress, or when the current anchor is invalid and
    `replace_invalid` is not set. The adoption is appended to the tenant's audit chain
    (`audit.retention_anchor_adopted`, naming the actor and the replaced anchor) before the
    anchor is written. If a retention sweep moves the anchor in between, the call returns
    409 and that event stays in the chain.

    Args:
        x_tenant_id (str):
        body (AdoptRetentionAnchorRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[AdoptRetentionAnchorResult, Any]
    """

    return sync_detailed(
        client=client,
        body=body,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: AdoptRetentionAnchorRequest,
    x_tenant_id: str,
) -> Response[Union[AdoptRetentionAnchorResult, Any]]:
    """Adopt a retention anchor for a chain trimmed outside the sweeper

     Writes the retention anchor that a tenant chain trimmed outside the retention sweeper (by
    hand, or by stream max-length eviction) lacks, so verify reports the missing prefix as
    `retention_trimmed` instead of `missing`. The anchor is the oldest retained event's
    predecessor: seq `expected_oldest_seq - 1`, the oldest event's `prev_hash`, and the stream
    ID just below its entry, HMAC-signed when `CORDUM_AUDIT_HMAC_KEY` is set. Admin-only.

    Refused with 409, changing nothing, when the oldest retained event is not
    `expected_oldest_seq`, when the current anchor already links the chain, when a Redis
    Cluster retention trim is in progress, or when the current anchor is invalid and
    `replace_invalid` is not set. The adoption is appended to the tenant's audit chain
    (`audit.retention_anchor_adopted`, naming the actor and the replaced anchor) before the
    anchor is written. If a retention sweep moves the anchor in between, the call returns
    409 and that event stays in the chain.

    Args:
        x_tenant_id (str):
        body (AdoptRetentionAnchorRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[AdoptRetentionAnchorResult, Any]]
    """

    kwargs = _get_kwargs(
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    body: AdoptRetentionAnchorRequest,
    x_tenant_id: str,
) -> Optional[Union[AdoptRetentionAnchorResult, Any]]:
    """Adopt a retention anchor for a chain trimmed outside the sweeper

     Writes the retention anchor that a tenant chain trimmed outside the retention sweeper (by
    hand, or by stream max-length eviction) lacks, so verify reports the missing prefix as
    `retention_trimmed` instead of `missing`. The anchor is the oldest retained event's
    predecessor: seq `expected_oldest_seq - 1`, the oldest event's `prev_hash`, and the stream
    ID just below its entry, HMAC-signed when `CORDUM_AUDIT_HMAC_KEY` is set. Admin-only.

    Refused with 409, changing nothing, when the oldest retained event is not
    `expected_oldest_seq`, when the current anchor already links the chain, when a Redis
    Cluster retention trim is in progress, or when the current anchor is invalid and
    `replace_invalid` is not set. The adoption is appended to the tenant's audit chain
    (`audit.retention_anchor_adopted`, naming the actor and the replaced anchor) before the
    anchor is written. If a retention sweep moves the anchor in between, the call returns
    409 and that event stays in the chain.

    Args:
        x_tenant_id (str):
        body (AdoptRetentionAnchorRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[AdoptRetentionAnchorResult, Any]
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
