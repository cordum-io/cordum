from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.revoke_shadow_exception_request import RevokeShadowExceptionRequest
from typing import cast
from typing import Dict


def _get_kwargs(
    exception_id: str,
    *,
    body: RevokeShadowExceptionRequest,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "delete",
        "url": "/api/v1/edge/shadow/exception/{exception_id}".format(
            exception_id=exception_id,
        ),
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Any]:
    if response.status_code == 204:
        return None
    if response.status_code == 400:
        return None
    if response.status_code == 401:
        return None
    if response.status_code == 403:
        return None
    if response.status_code == 404:
        return None
    if response.status_code == 409:
        return None
    if response.status_code == 503:
        return None
    if response.status_code == 500:
        return None
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Any]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    exception_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: RevokeShadowExceptionRequest,
    x_tenant_id: str,
) -> Response[Any]:
    r"""Revoke an active shadow exception (EDGE-143.6)

     Transitions an active exception to revoked. Revoke uses the SAME
    auth tier as the original create: if the exception's
    scope_risk_level is \"high\", the caller MUST satisfy the step-up
    gate. Failure returns 403 with code `step_up_required`.

    Idempotent when the exception is already revoked AND the caller
    principal matches the original revoker; conflicting double-revoke
    returns 409.

    Args:
        exception_id (str):
        x_tenant_id (str):
        body (RevokeShadowExceptionRequest): Optional body for revoking an exception. Empty body
            is allowed.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any]
    """

    kwargs = _get_kwargs(
        exception_id=exception_id,
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


async def asyncio_detailed(
    exception_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: RevokeShadowExceptionRequest,
    x_tenant_id: str,
) -> Response[Any]:
    r"""Revoke an active shadow exception (EDGE-143.6)

     Transitions an active exception to revoked. Revoke uses the SAME
    auth tier as the original create: if the exception's
    scope_risk_level is \"high\", the caller MUST satisfy the step-up
    gate. Failure returns 403 with code `step_up_required`.

    Idempotent when the exception is already revoked AND the caller
    principal matches the original revoker; conflicting double-revoke
    returns 409.

    Args:
        exception_id (str):
        x_tenant_id (str):
        body (RevokeShadowExceptionRequest): Optional body for revoking an exception. Empty body
            is allowed.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any]
    """

    kwargs = _get_kwargs(
        exception_id=exception_id,
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)
