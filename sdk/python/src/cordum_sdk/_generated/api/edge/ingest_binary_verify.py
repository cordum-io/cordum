from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.ingest_binary_verify_request import IngestBinaryVerifyRequest
from ...models.ingest_binary_verify_response import IngestBinaryVerifyResponse
from typing import cast
from typing import Dict


def _get_kwargs(
    *,
    body: IngestBinaryVerifyRequest,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/edge/binary-integrity/events",
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, IngestBinaryVerifyResponse]]:
    if response.status_code == 202:
        response_202 = IngestBinaryVerifyResponse.from_dict(response.json())

        return response_202
    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400
    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401
    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403
    if response.status_code == 413:
        response_413 = cast(Any, None)
        return response_413
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
) -> Response[Union[Any, IngestBinaryVerifyResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: IngestBinaryVerifyRequest,
    x_tenant_id: str,
) -> Response[Union[Any, IngestBinaryVerifyResponse]]:
    """Ingest install-time binary-verify outcomes

     Persists structured binary-verify outcomes emitted by
    `tools/scripts/install.{sh,ps1}` (the pre-activation integrity
    gate documented in `docs/security/binary-signing.md` §8).
    Operators capture install-script stderr (one JSON line per
    binary verified), batch it into the `events` array, and POST
    here. Each event is validated against `model.BinaryVerifyEvent`
    and persisted through the existing audit chain as a SIEMEvent
    with `EventType = binary-verify-ok | binary-verify-fail`.

    Partial-success semantics: per-event validation failures are
    reported in the response `errors[]` and counted in `rejected`;
    accepted events are persisted. A request with zero accepted
    events returns 400.

    Auth: requires `audit.export` permission or `admin` role,
    plus tenant access enforced via `X-Tenant-ID`.

    Args:
        x_tenant_id (str):
        body (IngestBinaryVerifyRequest): Batch envelope for binary-verify outcomes. Up to 1000
            events per
            request; the body byte size is capped at 2 MB.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, IngestBinaryVerifyResponse]]
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
    body: IngestBinaryVerifyRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, IngestBinaryVerifyResponse]]:
    """Ingest install-time binary-verify outcomes

     Persists structured binary-verify outcomes emitted by
    `tools/scripts/install.{sh,ps1}` (the pre-activation integrity
    gate documented in `docs/security/binary-signing.md` §8).
    Operators capture install-script stderr (one JSON line per
    binary verified), batch it into the `events` array, and POST
    here. Each event is validated against `model.BinaryVerifyEvent`
    and persisted through the existing audit chain as a SIEMEvent
    with `EventType = binary-verify-ok | binary-verify-fail`.

    Partial-success semantics: per-event validation failures are
    reported in the response `errors[]` and counted in `rejected`;
    accepted events are persisted. A request with zero accepted
    events returns 400.

    Auth: requires `audit.export` permission or `admin` role,
    plus tenant access enforced via `X-Tenant-ID`.

    Args:
        x_tenant_id (str):
        body (IngestBinaryVerifyRequest): Batch envelope for binary-verify outcomes. Up to 1000
            events per
            request; the body byte size is capped at 2 MB.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, IngestBinaryVerifyResponse]
    """

    return sync_detailed(
        client=client,
        body=body,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: IngestBinaryVerifyRequest,
    x_tenant_id: str,
) -> Response[Union[Any, IngestBinaryVerifyResponse]]:
    """Ingest install-time binary-verify outcomes

     Persists structured binary-verify outcomes emitted by
    `tools/scripts/install.{sh,ps1}` (the pre-activation integrity
    gate documented in `docs/security/binary-signing.md` §8).
    Operators capture install-script stderr (one JSON line per
    binary verified), batch it into the `events` array, and POST
    here. Each event is validated against `model.BinaryVerifyEvent`
    and persisted through the existing audit chain as a SIEMEvent
    with `EventType = binary-verify-ok | binary-verify-fail`.

    Partial-success semantics: per-event validation failures are
    reported in the response `errors[]` and counted in `rejected`;
    accepted events are persisted. A request with zero accepted
    events returns 400.

    Auth: requires `audit.export` permission or `admin` role,
    plus tenant access enforced via `X-Tenant-ID`.

    Args:
        x_tenant_id (str):
        body (IngestBinaryVerifyRequest): Batch envelope for binary-verify outcomes. Up to 1000
            events per
            request; the body byte size is capped at 2 MB.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, IngestBinaryVerifyResponse]]
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
    body: IngestBinaryVerifyRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, IngestBinaryVerifyResponse]]:
    """Ingest install-time binary-verify outcomes

     Persists structured binary-verify outcomes emitted by
    `tools/scripts/install.{sh,ps1}` (the pre-activation integrity
    gate documented in `docs/security/binary-signing.md` §8).
    Operators capture install-script stderr (one JSON line per
    binary verified), batch it into the `events` array, and POST
    here. Each event is validated against `model.BinaryVerifyEvent`
    and persisted through the existing audit chain as a SIEMEvent
    with `EventType = binary-verify-ok | binary-verify-fail`.

    Partial-success semantics: per-event validation failures are
    reported in the response `errors[]` and counted in `rejected`;
    accepted events are persisted. A request with zero accepted
    events returns 400.

    Auth: requires `audit.export` permission or `admin` role,
    plus tenant access enforced via `X-Tenant-ID`.

    Args:
        x_tenant_id (str):
        body (IngestBinaryVerifyRequest): Batch envelope for binary-verify outcomes. Up to 1000
            events per
            request; the body byte size is capped at 2 MB.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, IngestBinaryVerifyResponse]
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
