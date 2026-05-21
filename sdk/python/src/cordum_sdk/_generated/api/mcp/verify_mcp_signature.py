from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.verify_mcp_signature_body import VerifyMcpSignatureBody
from ...models.verify_mcp_signature_response_200 import VerifyMcpSignatureResponse200
from typing import cast
from typing import Dict


def _get_kwargs(
    *,
    body: VerifyMcpSignatureBody,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/mcp/verify-signature",
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, VerifyMcpSignatureResponse200]]:
    if response.status_code == 200:
        response_200 = VerifyMcpSignatureResponse200.from_dict(response.json())

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
    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, VerifyMcpSignatureResponse200]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: VerifyMcpSignatureBody,
) -> Response[Union[Any, VerifyMcpSignatureResponse200]]:
    """Verify an outbound MCP signature (admin-gated)

     Verify an ECDSA signature over an MCP request against the trusted-key store. Admin-only. See
    docs/mcp/outbound-signing.md.

    Args:
        body (VerifyMcpSignatureBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, VerifyMcpSignatureResponse200]]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: VerifyMcpSignatureBody,
) -> Optional[Union[Any, VerifyMcpSignatureResponse200]]:
    """Verify an outbound MCP signature (admin-gated)

     Verify an ECDSA signature over an MCP request against the trusted-key store. Admin-only. See
    docs/mcp/outbound-signing.md.

    Args:
        body (VerifyMcpSignatureBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, VerifyMcpSignatureResponse200]
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: VerifyMcpSignatureBody,
) -> Response[Union[Any, VerifyMcpSignatureResponse200]]:
    """Verify an outbound MCP signature (admin-gated)

     Verify an ECDSA signature over an MCP request against the trusted-key store. Admin-only. See
    docs/mcp/outbound-signing.md.

    Args:
        body (VerifyMcpSignatureBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, VerifyMcpSignatureResponse200]]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: VerifyMcpSignatureBody,
) -> Optional[Union[Any, VerifyMcpSignatureResponse200]]:
    """Verify an outbound MCP signature (admin-gated)

     Verify an ECDSA signature over an MCP request against the trusted-key store. Admin-only. See
    docs/mcp/outbound-signing.md.

    Args:
        body (VerifyMcpSignatureBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, VerifyMcpSignatureResponse200]
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
