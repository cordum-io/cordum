from http import HTTPStatus
from typing import Any, Dict, Optional, Union, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.policy_shadow import PolicyShadow
from ...models.policy_shadow_upsert_request import PolicyShadowUpsertRequest
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: PolicyShadowUpsertRequest,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "put",
        "url": "/api/v1/policy/shadows/{id}".format(
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
) -> Optional[Union[Any, PolicyShadow]]:
    if response.status_code == 200:
        response_200 = PolicyShadow.from_dict(response.json())

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
) -> Response[Union[Any, PolicyShadow]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient,
    body: PolicyShadowUpsertRequest,
    x_tenant_id: str,
) -> Response[Union[Any, PolicyShadow]]:
    """Activate or replace the shadow policy for a bundle

     PUT upserts the shadow policy for the given bundle. A bundle has at
    most one shadow at a time; re-activating replaces the current one
    in place. Validation matches `handlePutPolicyBundle` — malformed
    YAML returns 400 before any Redis write.

    Args:
        id (str):
        x_tenant_id (str):
        body (PolicyShadowUpsertRequest): PUT body for activating or replacing a shadow policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, PolicyShadow]]
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
    client: AuthenticatedClient,
    body: PolicyShadowUpsertRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, PolicyShadow]]:
    """Activate or replace the shadow policy for a bundle

     PUT upserts the shadow policy for the given bundle. A bundle has at
    most one shadow at a time; re-activating replaces the current one
    in place. Validation matches `handlePutPolicyBundle` — malformed
    YAML returns 400 before any Redis write.

    Args:
        id (str):
        x_tenant_id (str):
        body (PolicyShadowUpsertRequest): PUT body for activating or replacing a shadow policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, PolicyShadow]
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
    client: AuthenticatedClient,
    body: PolicyShadowUpsertRequest,
    x_tenant_id: str,
) -> Response[Union[Any, PolicyShadow]]:
    """Activate or replace the shadow policy for a bundle

     PUT upserts the shadow policy for the given bundle. A bundle has at
    most one shadow at a time; re-activating replaces the current one
    in place. Validation matches `handlePutPolicyBundle` — malformed
    YAML returns 400 before any Redis write.

    Args:
        id (str):
        x_tenant_id (str):
        body (PolicyShadowUpsertRequest): PUT body for activating or replacing a shadow policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, PolicyShadow]]
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
    client: AuthenticatedClient,
    body: PolicyShadowUpsertRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, PolicyShadow]]:
    """Activate or replace the shadow policy for a bundle

     PUT upserts the shadow policy for the given bundle. A bundle has at
    most one shadow at a time; re-activating replaces the current one
    in place. Validation matches `handlePutPolicyBundle` — malformed
    YAML returns 400 before any Redis write.

    Args:
        id (str):
        x_tenant_id (str):
        body (PolicyShadowUpsertRequest): PUT body for activating or replacing a shadow policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, PolicyShadow]
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
