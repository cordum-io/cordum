from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.edge_approval import EdgeApproval
from ...models.edge_error import EdgeError
from ...models.wait_edge_approval_body import WaitEdgeApprovalBody
from typing import cast
from typing import Dict


def _get_kwargs(
    approval_ref: str,
    *,
    body: WaitEdgeApprovalBody,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/edge/approvals/{approval_ref}/wait".format(
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
        response_403 = cast(Any, None)
        return response_403
    if response.status_code == 404:
        response_404 = EdgeError.from_dict(response.json())

        return response_404
    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409
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
    body: WaitEdgeApprovalBody,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeApproval, EdgeError]]:
    """Bounded blocking wait for an Edge approval to leave Pending

     Agentd/local-dev affordance that polls the Edge approval store until the approval is non-pending or
    the bounded timeout elapses, then returns the current EdgeApproval. The wait is principal-bound: the
    caller must be the original requester principal (`auth.PrincipalID` matches the approval
    `principal_id`) or hold an admin/operator role. The server clamps `timeout_ms` to a 5 minute maximum
    and uses a 30 second default when omitted or non-positive. Tenant isolation is enforced; cross-
    tenant references and same-tenant callers that fail the principal binding return 404 with no
    metadata leakage. This endpoint is not required by the dashboard approve/reject UX — callers there
    should use the standard list/detail/approve/reject flow.

    Args:
        approval_ref (str):
        x_tenant_id (str):
        body (WaitEdgeApprovalBody):

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
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    approval_ref: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: WaitEdgeApprovalBody,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeApproval, EdgeError]]:
    """Bounded blocking wait for an Edge approval to leave Pending

     Agentd/local-dev affordance that polls the Edge approval store until the approval is non-pending or
    the bounded timeout elapses, then returns the current EdgeApproval. The wait is principal-bound: the
    caller must be the original requester principal (`auth.PrincipalID` matches the approval
    `principal_id`) or hold an admin/operator role. The server clamps `timeout_ms` to a 5 minute maximum
    and uses a 30 second default when omitted or non-positive. Tenant isolation is enforced; cross-
    tenant references and same-tenant callers that fail the principal binding return 404 with no
    metadata leakage. This endpoint is not required by the dashboard approve/reject UX — callers there
    should use the standard list/detail/approve/reject flow.

    Args:
        approval_ref (str):
        x_tenant_id (str):
        body (WaitEdgeApprovalBody):

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
    ).parsed


async def asyncio_detailed(
    approval_ref: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: WaitEdgeApprovalBody,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeApproval, EdgeError]]:
    """Bounded blocking wait for an Edge approval to leave Pending

     Agentd/local-dev affordance that polls the Edge approval store until the approval is non-pending or
    the bounded timeout elapses, then returns the current EdgeApproval. The wait is principal-bound: the
    caller must be the original requester principal (`auth.PrincipalID` matches the approval
    `principal_id`) or hold an admin/operator role. The server clamps `timeout_ms` to a 5 minute maximum
    and uses a 30 second default when omitted or non-positive. Tenant isolation is enforced; cross-
    tenant references and same-tenant callers that fail the principal binding return 404 with no
    metadata leakage. This endpoint is not required by the dashboard approve/reject UX — callers there
    should use the standard list/detail/approve/reject flow.

    Args:
        approval_ref (str):
        x_tenant_id (str):
        body (WaitEdgeApprovalBody):

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
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    approval_ref: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: WaitEdgeApprovalBody,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeApproval, EdgeError]]:
    """Bounded blocking wait for an Edge approval to leave Pending

     Agentd/local-dev affordance that polls the Edge approval store until the approval is non-pending or
    the bounded timeout elapses, then returns the current EdgeApproval. The wait is principal-bound: the
    caller must be the original requester principal (`auth.PrincipalID` matches the approval
    `principal_id`) or hold an admin/operator role. The server clamps `timeout_ms` to a 5 minute maximum
    and uses a 30 second default when omitted or non-positive. Tenant isolation is enforced; cross-
    tenant references and same-tenant callers that fail the principal binding return 404 with no
    metadata leakage. This endpoint is not required by the dashboard approve/reject UX — callers there
    should use the standard list/detail/approve/reject flow.

    Args:
        approval_ref (str):
        x_tenant_id (str):
        body (WaitEdgeApprovalBody):

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
        )
    ).parsed
