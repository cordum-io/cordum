from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.shadow_agent_remediation_request import ShadowAgentRemediationRequest
from ...models.shadow_agent_remediation_response import ShadowAgentRemediationResponse
from typing import cast
from typing import Dict


def _get_kwargs(
    finding_id: str,
    *,
    body: ShadowAgentRemediationRequest,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/edge/shadow-agents/{finding_id}/remediation".format(
            finding_id=finding_id,
        ),
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, ShadowAgentRemediationResponse]]:
    if response.status_code == 200:
        response_200 = ShadowAgentRemediationResponse.from_dict(response.json())

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
        response_404 = cast(Any, None)
        return response_404
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
) -> Response[Union[Any, ShadowAgentRemediationResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    finding_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: ShadowAgentRemediationRequest,
    x_tenant_id: str,
) -> Response[Union[Any, ShadowAgentRemediationResponse]]:
    """Generate an advisory remediation plan for a shadow-agent finding (EDGE-142)

     Returns a deterministic, redacted RemediationPlan for the referenced finding.
    Read-only: the handler does not mutate finding state, does not enqueue Cordum
    Jobs, does not emit audit events, and does not call the Safety Kernel. All
    commands inside the plan use literal placeholders (`<gateway-url>`,
    `<tenant-id>`, etc.) and never carry live secrets or developer paths.

    Args:
        finding_id (str):
        x_tenant_id (str):
        body (ShadowAgentRemediationRequest): Optional body for generating a remediation plan.
            Both fields default to the generator's documented defaults when omitted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ShadowAgentRemediationResponse]]
    """

    kwargs = _get_kwargs(
        finding_id=finding_id,
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    finding_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: ShadowAgentRemediationRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, ShadowAgentRemediationResponse]]:
    """Generate an advisory remediation plan for a shadow-agent finding (EDGE-142)

     Returns a deterministic, redacted RemediationPlan for the referenced finding.
    Read-only: the handler does not mutate finding state, does not enqueue Cordum
    Jobs, does not emit audit events, and does not call the Safety Kernel. All
    commands inside the plan use literal placeholders (`<gateway-url>`,
    `<tenant-id>`, etc.) and never carry live secrets or developer paths.

    Args:
        finding_id (str):
        x_tenant_id (str):
        body (ShadowAgentRemediationRequest): Optional body for generating a remediation plan.
            Both fields default to the generator's documented defaults when omitted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ShadowAgentRemediationResponse]
    """

    return sync_detailed(
        finding_id=finding_id,
        client=client,
        body=body,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    finding_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: ShadowAgentRemediationRequest,
    x_tenant_id: str,
) -> Response[Union[Any, ShadowAgentRemediationResponse]]:
    """Generate an advisory remediation plan for a shadow-agent finding (EDGE-142)

     Returns a deterministic, redacted RemediationPlan for the referenced finding.
    Read-only: the handler does not mutate finding state, does not enqueue Cordum
    Jobs, does not emit audit events, and does not call the Safety Kernel. All
    commands inside the plan use literal placeholders (`<gateway-url>`,
    `<tenant-id>`, etc.) and never carry live secrets or developer paths.

    Args:
        finding_id (str):
        x_tenant_id (str):
        body (ShadowAgentRemediationRequest): Optional body for generating a remediation plan.
            Both fields default to the generator's documented defaults when omitted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ShadowAgentRemediationResponse]]
    """

    kwargs = _get_kwargs(
        finding_id=finding_id,
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    finding_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: ShadowAgentRemediationRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, ShadowAgentRemediationResponse]]:
    """Generate an advisory remediation plan for a shadow-agent finding (EDGE-142)

     Returns a deterministic, redacted RemediationPlan for the referenced finding.
    Read-only: the handler does not mutate finding state, does not enqueue Cordum
    Jobs, does not emit audit events, and does not call the Safety Kernel. All
    commands inside the plan use literal placeholders (`<gateway-url>`,
    `<tenant-id>`, etc.) and never carry live secrets or developer paths.

    Args:
        finding_id (str):
        x_tenant_id (str):
        body (ShadowAgentRemediationRequest): Optional body for generating a remediation plan.
            Both fields default to the generator's documented defaults when omitted.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ShadowAgentRemediationResponse]
    """

    return (
        await asyncio_detailed(
            finding_id=finding_id,
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
