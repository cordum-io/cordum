from http import HTTPStatus
from typing import Any, Dict, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response
from ... import errors

from ...models.error import Error
from ...models.output_rule_toggle_request import OutputRuleToggleRequest
from ...models.output_rule_toggle_result import OutputRuleToggleResult
from typing import cast
from typing import Dict


def _get_kwargs(
    id: str,
    *,
    body: OutputRuleToggleRequest,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "put",
        "url": "/api/v1/policy/output/rules/{id}".format(
            id=id,
        ),
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, Error, OutputRuleToggleResult]]:
    if response.status_code == 200:
        response_200 = OutputRuleToggleResult.from_dict(response.json())

        return response_200
    if response.status_code == 400:
        response_400 = Error.from_dict(response.json())

        return response_400
    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401
    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403
    if response.status_code == 404:
        response_404 = Error.from_dict(response.json())

        return response_404
    if response.status_code == 409:
        response_409 = Error.from_dict(response.json())

        return response_409
    if response.status_code == 500:
        response_500 = cast(Any, None)
        return response_500
    if response.status_code == 503:
        response_503 = Error.from_dict(response.json())

        return response_503
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, Error, OutputRuleToggleResult]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: OutputRuleToggleRequest,
    x_tenant_id: str,
) -> Response[Union[Any, Error, OutputRuleToggleResult]]:
    """Enable or disable an existing output policy rule

     Sets `enabled` on an existing output rule and writes back the stored
    policy bundle that holds it, with an audit entry. Bundles are searched
    in key order, and the first one whose content holds the rule id is
    changed. This route does not create a rule or change any other rule
    field; the policy bundle routes do that.

    Before it writes, the gateway checks the bundle's stored content
    against the bundle's stored `_signature` and the policy trust store.
    - Under the enforce signature mode, a bundle that verifies is re-signed
      with the configured policy signing key. A bundle that does not
      verify is refused with `409` and nothing is written.
    - In warn or off mode, the bundle is re-signed only when its stored
      content verifies and a policy signing key loads. Otherwise the
      toggled content is stored unsigned and any stale `_signature` is
      removed.

    The enforce mode applies when `CORDUM_POLICY_STRICT` is `enforce` or
    holds an invalid value. When it is unset, enforce applies in the
    production profile or when `SAFETY_POLICY_SIGNATURE_REQUIRED` is true.

    Args:
        id (str):
        x_tenant_id (str):
        body (OutputRuleToggleRequest): Body of `PUT /api/v1/policy/output/rules/{id}`. `enabled`
            is the only accepted field, and the gateway requires it: a body without it gets `400`
            `POLICY_VALIDATION_FAILED`, and a body with any other field gets `400`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, Error, OutputRuleToggleResult]]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: OutputRuleToggleRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, Error, OutputRuleToggleResult]]:
    """Enable or disable an existing output policy rule

     Sets `enabled` on an existing output rule and writes back the stored
    policy bundle that holds it, with an audit entry. Bundles are searched
    in key order, and the first one whose content holds the rule id is
    changed. This route does not create a rule or change any other rule
    field; the policy bundle routes do that.

    Before it writes, the gateway checks the bundle's stored content
    against the bundle's stored `_signature` and the policy trust store.
    - Under the enforce signature mode, a bundle that verifies is re-signed
      with the configured policy signing key. A bundle that does not
      verify is refused with `409` and nothing is written.
    - In warn or off mode, the bundle is re-signed only when its stored
      content verifies and a policy signing key loads. Otherwise the
      toggled content is stored unsigned and any stale `_signature` is
      removed.

    The enforce mode applies when `CORDUM_POLICY_STRICT` is `enforce` or
    holds an invalid value. When it is unset, enforce applies in the
    production profile or when `SAFETY_POLICY_SIGNATURE_REQUIRED` is true.

    Args:
        id (str):
        x_tenant_id (str):
        body (OutputRuleToggleRequest): Body of `PUT /api/v1/policy/output/rules/{id}`. `enabled`
            is the only accepted field, and the gateway requires it: a body without it gets `400`
            `POLICY_VALIDATION_FAILED`, and a body with any other field gets `400`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, Error, OutputRuleToggleResult]
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: OutputRuleToggleRequest,
    x_tenant_id: str,
) -> Response[Union[Any, Error, OutputRuleToggleResult]]:
    """Enable or disable an existing output policy rule

     Sets `enabled` on an existing output rule and writes back the stored
    policy bundle that holds it, with an audit entry. Bundles are searched
    in key order, and the first one whose content holds the rule id is
    changed. This route does not create a rule or change any other rule
    field; the policy bundle routes do that.

    Before it writes, the gateway checks the bundle's stored content
    against the bundle's stored `_signature` and the policy trust store.
    - Under the enforce signature mode, a bundle that verifies is re-signed
      with the configured policy signing key. A bundle that does not
      verify is refused with `409` and nothing is written.
    - In warn or off mode, the bundle is re-signed only when its stored
      content verifies and a policy signing key loads. Otherwise the
      toggled content is stored unsigned and any stale `_signature` is
      removed.

    The enforce mode applies when `CORDUM_POLICY_STRICT` is `enforce` or
    holds an invalid value. When it is unset, enforce applies in the
    production profile or when `SAFETY_POLICY_SIGNATURE_REQUIRED` is true.

    Args:
        id (str):
        x_tenant_id (str):
        body (OutputRuleToggleRequest): Body of `PUT /api/v1/policy/output/rules/{id}`. `enabled`
            is the only accepted field, and the gateway requires it: a body without it gets `400`
            `POLICY_VALIDATION_FAILED`, and a body with any other field gets `400`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, Error, OutputRuleToggleResult]]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: OutputRuleToggleRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, Error, OutputRuleToggleResult]]:
    """Enable or disable an existing output policy rule

     Sets `enabled` on an existing output rule and writes back the stored
    policy bundle that holds it, with an audit entry. Bundles are searched
    in key order, and the first one whose content holds the rule id is
    changed. This route does not create a rule or change any other rule
    field; the policy bundle routes do that.

    Before it writes, the gateway checks the bundle's stored content
    against the bundle's stored `_signature` and the policy trust store.
    - Under the enforce signature mode, a bundle that verifies is re-signed
      with the configured policy signing key. A bundle that does not
      verify is refused with `409` and nothing is written.
    - In warn or off mode, the bundle is re-signed only when its stored
      content verifies and a policy signing key loads. Otherwise the
      toggled content is stored unsigned and any stale `_signature` is
      removed.

    The enforce mode applies when `CORDUM_POLICY_STRICT` is `enforce` or
    holds an invalid value. When it is unset, enforce applies in the
    production profile or when `SAFETY_POLICY_SIGNATURE_REQUIRED` is true.

    Args:
        id (str):
        x_tenant_id (str):
        body (OutputRuleToggleRequest): Body of `PUT /api/v1/policy/output/rules/{id}`. `enabled`
            is the only accepted field, and the gateway requires it: a body without it gets `400`
            `POLICY_VALIDATION_FAILED`, and a body with any other field gets `400`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, Error, OutputRuleToggleResult]
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
