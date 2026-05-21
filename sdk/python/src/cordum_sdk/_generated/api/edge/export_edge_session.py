from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.edge_error import EdgeError
from ...models.export_edge_session_body import ExportEdgeSessionBody
from ...models.export_edge_session_response_200 import ExportEdgeSessionResponse200
from typing import cast
from typing import Dict


def _get_kwargs(
    session_id: str,
    *,
    body: ExportEdgeSessionBody,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/edge/sessions/{session_id}/export".format(
            session_id=session_id,
        ),
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, EdgeError, ExportEdgeSessionResponse200]]:
    if response.status_code == 200:
        response_200 = ExportEdgeSessionResponse200.from_dict(response.json())

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
    if response.status_code == 413:
        response_413 = EdgeError.from_dict(response.json())

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
) -> Response[Union[Any, EdgeError, ExportEdgeSessionResponse200]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    session_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: ExportEdgeSessionBody,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeError, ExportEdgeSessionResponse200]]:
    """Assemble an Edge session evidence bundle

     Returns a `SessionExportBundle` for the named session — session record,
    executions, ordered AgentActionEvents, approvals, and a metadata-only
    manifest of every artifact pointer attached to those events. Bundles
    contain artifact metadata (URI, sha256, size, content_type, retention,
    redaction level) but never raw artifact bodies. Cross-tenant probes
    receive 404 indistinguishable from missing-session.

    Args:
        session_id (str):
        x_tenant_id (str):
        body (ExportEdgeSessionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeError, ExportEdgeSessionResponse200]]
    """

    kwargs = _get_kwargs(
        session_id=session_id,
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    session_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: ExportEdgeSessionBody,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeError, ExportEdgeSessionResponse200]]:
    """Assemble an Edge session evidence bundle

     Returns a `SessionExportBundle` for the named session — session record,
    executions, ordered AgentActionEvents, approvals, and a metadata-only
    manifest of every artifact pointer attached to those events. Bundles
    contain artifact metadata (URI, sha256, size, content_type, retention,
    redaction level) but never raw artifact bodies. Cross-tenant probes
    receive 404 indistinguishable from missing-session.

    Args:
        session_id (str):
        x_tenant_id (str):
        body (ExportEdgeSessionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeError, ExportEdgeSessionResponse200]
    """

    return sync_detailed(
        session_id=session_id,
        client=client,
        body=body,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    session_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: ExportEdgeSessionBody,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeError, ExportEdgeSessionResponse200]]:
    """Assemble an Edge session evidence bundle

     Returns a `SessionExportBundle` for the named session — session record,
    executions, ordered AgentActionEvents, approvals, and a metadata-only
    manifest of every artifact pointer attached to those events. Bundles
    contain artifact metadata (URI, sha256, size, content_type, retention,
    redaction level) but never raw artifact bodies. Cross-tenant probes
    receive 404 indistinguishable from missing-session.

    Args:
        session_id (str):
        x_tenant_id (str):
        body (ExportEdgeSessionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeError, ExportEdgeSessionResponse200]]
    """

    kwargs = _get_kwargs(
        session_id=session_id,
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    session_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    body: ExportEdgeSessionBody,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeError, ExportEdgeSessionResponse200]]:
    """Assemble an Edge session evidence bundle

     Returns a `SessionExportBundle` for the named session — session record,
    executions, ordered AgentActionEvents, approvals, and a metadata-only
    manifest of every artifact pointer attached to those events. Bundles
    contain artifact metadata (URI, sha256, size, content_type, retention,
    redaction level) but never raw artifact bodies. Cross-tenant probes
    receive 404 indistinguishable from missing-session.

    Args:
        session_id (str):
        x_tenant_id (str):
        body (ExportEdgeSessionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeError, ExportEdgeSessionResponse200]
    """

    return (
        await asyncio_detailed(
            session_id=session_id,
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
