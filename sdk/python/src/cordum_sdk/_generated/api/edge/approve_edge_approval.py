from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.edge_approval import EdgeApproval
from ...models.edge_approval_decision_request import EdgeApprovalDecisionRequest
from ...models.edge_error import EdgeError
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union
from uuid import UUID


def _get_kwargs(
    approval_ref: str,
    *,
    body: EdgeApprovalDecisionRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/edge/approvals/{approval_ref}/approve".format(
            approval_ref=approval_ref,
        ),
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, EdgeApproval, EdgeError]]:
    if response.status_code == 200:
        response_200 = EdgeApproval.from_dict(response.json())

        return response_200
    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400
    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401
    if response.status_code == 403:
        response_403 = EdgeError.from_dict(response.json())

        return response_403
    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404
    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409
    if response.status_code == 413:
        response_413 = cast(Any, None)
        return response_413
    if response.status_code == 429:
        response_429 = cast(Any, None)
        return response_429
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
) -> Response[Union[Any, EdgeApproval, EdgeError]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    approval_ref: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeApprovalDecisionRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Response[Union[Any, EdgeApproval, EdgeError]]:
    r"""Approve an Edge action approval

     Resolves a pending Edge approval as approved for one retry/consume. Requires approval/admin
    permission, enforces tenant scope and self-approval protection, and fails with 409 if the
    session/execution/event/action hash or policy snapshot is stale.

    EDGE-060 — supports `Idempotency-Key` for safe retry against
    the resolution state transition. Same key + same body returns
    the SAME cached 200 response (replay), NOT 409 \"already
    approved\" — this is the EDGE-060 DoD #7 invariant. A different
    key against an already-terminal approval still surfaces the
    existing 409 path.

    Args:
        approval_ref (str):
        x_tenant_id (str):
        idempotency_key (Union[Unset, UUID]):
        body (EdgeApprovalDecisionRequest): Human resolver note for approving or rejecting an Edge
            action approval.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeApproval, EdgeError]]
    """

    kwargs = _get_kwargs(
        approval_ref=approval_ref,
        body=body,
        x_tenant_id=x_tenant_id,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    approval_ref: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeApprovalDecisionRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Optional[Union[Any, EdgeApproval, EdgeError]]:
    r"""Approve an Edge action approval

     Resolves a pending Edge approval as approved for one retry/consume. Requires approval/admin
    permission, enforces tenant scope and self-approval protection, and fails with 409 if the
    session/execution/event/action hash or policy snapshot is stale.

    EDGE-060 — supports `Idempotency-Key` for safe retry against
    the resolution state transition. Same key + same body returns
    the SAME cached 200 response (replay), NOT 409 \"already
    approved\" — this is the EDGE-060 DoD #7 invariant. A different
    key against an already-terminal approval still surfaces the
    existing 409 path.

    Args:
        approval_ref (str):
        x_tenant_id (str):
        idempotency_key (Union[Unset, UUID]):
        body (EdgeApprovalDecisionRequest): Human resolver note for approving or rejecting an Edge
            action approval.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeApproval, EdgeError]
    """

    return sync_detailed(
        approval_ref=approval_ref,
        client=client,
        body=body,
        x_tenant_id=x_tenant_id,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    approval_ref: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeApprovalDecisionRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Response[Union[Any, EdgeApproval, EdgeError]]:
    r"""Approve an Edge action approval

     Resolves a pending Edge approval as approved for one retry/consume. Requires approval/admin
    permission, enforces tenant scope and self-approval protection, and fails with 409 if the
    session/execution/event/action hash or policy snapshot is stale.

    EDGE-060 — supports `Idempotency-Key` for safe retry against
    the resolution state transition. Same key + same body returns
    the SAME cached 200 response (replay), NOT 409 \"already
    approved\" — this is the EDGE-060 DoD #7 invariant. A different
    key against an already-terminal approval still surfaces the
    existing 409 path.

    Args:
        approval_ref (str):
        x_tenant_id (str):
        idempotency_key (Union[Unset, UUID]):
        body (EdgeApprovalDecisionRequest): Human resolver note for approving or rejecting an Edge
            action approval.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeApproval, EdgeError]]
    """

    kwargs = _get_kwargs(
        approval_ref=approval_ref,
        body=body,
        x_tenant_id=x_tenant_id,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    approval_ref: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeApprovalDecisionRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Optional[Union[Any, EdgeApproval, EdgeError]]:
    r"""Approve an Edge action approval

     Resolves a pending Edge approval as approved for one retry/consume. Requires approval/admin
    permission, enforces tenant scope and self-approval protection, and fails with 409 if the
    session/execution/event/action hash or policy snapshot is stale.

    EDGE-060 — supports `Idempotency-Key` for safe retry against
    the resolution state transition. Same key + same body returns
    the SAME cached 200 response (replay), NOT 409 \"already
    approved\" — this is the EDGE-060 DoD #7 invariant. A different
    key against an already-terminal approval still surfaces the
    existing 409 path.

    Args:
        approval_ref (str):
        x_tenant_id (str):
        idempotency_key (Union[Unset, UUID]):
        body (EdgeApprovalDecisionRequest): Human resolver note for approving or rejecting an Edge
            action approval.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeApproval, EdgeError]
    """

    return (
        await asyncio_detailed(
            approval_ref=approval_ref,
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
            idempotency_key=idempotency_key,
        )
    ).parsed
