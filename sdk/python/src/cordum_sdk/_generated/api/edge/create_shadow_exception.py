from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.create_shadow_exception_request import CreateShadowExceptionRequest
from ...models.edge_error import EdgeError
from ...models.shadow_exception import ShadowException
from typing import cast
from typing import Dict


def _get_kwargs(
    *,
    body: CreateShadowExceptionRequest,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/edge/shadow/exception",
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, EdgeError, ShadowException]]:
    if response.status_code == 201:
        response_201 = ShadowException.from_dict(response.json())

        return response_201
    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400
    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401
    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403
    if response.status_code == 429:
        response_429 = EdgeError.from_dict(response.json())

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
) -> Response[Union[Any, EdgeError, ShadowException]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: CreateShadowExceptionRequest,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeError, ShadowException]]:
    r"""Create an operator-defined shadow exception (EDGE-143.6)

     Persists an operator-signed exception declaration that suppresses
    future ShadowAgent findings matching its scope predicate
    (source_type + source_id + risk_level + signal_set). Matching is
    applied at finding emit time: matching findings are stamped with
    exception_id, false_positive_reason=\"operator_exception\", and
    status=managed_skip; they are excluded from default-filter list
    queries.

    Q8 step-up auth: when scope_risk_level is \"high\", the caller MUST
    hold the `admin` legacy role OR the `shadow.exception.high_risk`
    permission. Failure returns 403 with code `step_up_required` and
    details.required = \"mfa_recent|signed_admin_token\". The persisted
    Exception records which factor satisfied the gate so SIEM rules
    can pivot on the auth tier at the time of action.

    expires_at MUST be in the future and within 90 days (§10.3
    \"longer requires re-affirmation\").

    Args:
        x_tenant_id (str):
        body (CreateShadowExceptionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeError, ShadowException]]
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
    body: CreateShadowExceptionRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeError, ShadowException]]:
    r"""Create an operator-defined shadow exception (EDGE-143.6)

     Persists an operator-signed exception declaration that suppresses
    future ShadowAgent findings matching its scope predicate
    (source_type + source_id + risk_level + signal_set). Matching is
    applied at finding emit time: matching findings are stamped with
    exception_id, false_positive_reason=\"operator_exception\", and
    status=managed_skip; they are excluded from default-filter list
    queries.

    Q8 step-up auth: when scope_risk_level is \"high\", the caller MUST
    hold the `admin` legacy role OR the `shadow.exception.high_risk`
    permission. Failure returns 403 with code `step_up_required` and
    details.required = \"mfa_recent|signed_admin_token\". The persisted
    Exception records which factor satisfied the gate so SIEM rules
    can pivot on the auth tier at the time of action.

    expires_at MUST be in the future and within 90 days (§10.3
    \"longer requires re-affirmation\").

    Args:
        x_tenant_id (str):
        body (CreateShadowExceptionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeError, ShadowException]
    """

    return sync_detailed(
        client=client,
        body=body,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: CreateShadowExceptionRequest,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeError, ShadowException]]:
    r"""Create an operator-defined shadow exception (EDGE-143.6)

     Persists an operator-signed exception declaration that suppresses
    future ShadowAgent findings matching its scope predicate
    (source_type + source_id + risk_level + signal_set). Matching is
    applied at finding emit time: matching findings are stamped with
    exception_id, false_positive_reason=\"operator_exception\", and
    status=managed_skip; they are excluded from default-filter list
    queries.

    Q8 step-up auth: when scope_risk_level is \"high\", the caller MUST
    hold the `admin` legacy role OR the `shadow.exception.high_risk`
    permission. Failure returns 403 with code `step_up_required` and
    details.required = \"mfa_recent|signed_admin_token\". The persisted
    Exception records which factor satisfied the gate so SIEM rules
    can pivot on the auth tier at the time of action.

    expires_at MUST be in the future and within 90 days (§10.3
    \"longer requires re-affirmation\").

    Args:
        x_tenant_id (str):
        body (CreateShadowExceptionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeError, ShadowException]]
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
    body: CreateShadowExceptionRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeError, ShadowException]]:
    r"""Create an operator-defined shadow exception (EDGE-143.6)

     Persists an operator-signed exception declaration that suppresses
    future ShadowAgent findings matching its scope predicate
    (source_type + source_id + risk_level + signal_set). Matching is
    applied at finding emit time: matching findings are stamped with
    exception_id, false_positive_reason=\"operator_exception\", and
    status=managed_skip; they are excluded from default-filter list
    queries.

    Q8 step-up auth: when scope_risk_level is \"high\", the caller MUST
    hold the `admin` legacy role OR the `shadow.exception.high_risk`
    permission. Failure returns 403 with code `step_up_required` and
    details.required = \"mfa_recent|signed_admin_token\". The persisted
    Exception records which factor satisfied the gate so SIEM rules
    can pivot on the auth tier at the time of action.

    expires_at MUST be in the future and within 90 days (§10.3
    \"longer requires re-affirmation\").

    Args:
        x_tenant_id (str):
        body (CreateShadowExceptionRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeError, ShadowException]
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
