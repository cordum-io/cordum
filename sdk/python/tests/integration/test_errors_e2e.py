from __future__ import annotations

import pytest

from cordum_sdk import CordumClient
from cordum_sdk.errors import NotFoundError, ValidationError


def test_validation_error_envelope_populates_field_errors(respx_router, gateway_state) -> None:
    del respx_router
    gateway_state.validation_error_job_id = "job-invalid"

    client = CordumClient(
        "https://api.example.test",
        auth="good-api-key",
        tenant_id="tenant-123",
    )

    with pytest.raises(ValidationError) as exc_info:
        client.jobs.get_job("job-invalid")
    client.close()

    assert exc_info.value.field_errors == {"prompt": "prompt is required"}
    assert exc_info.value.request_id == "req-validation"


def test_not_found_preserves_request_id(respx_router, gateway_state) -> None:
    del respx_router, gateway_state
    client = CordumClient(
        "https://api.example.test",
        auth="good-api-key",
        tenant_id="tenant-123",
    )

    with pytest.raises(NotFoundError) as exc_info:
        client.jobs.get_job("missing-job")
    client.close()

    assert exc_info.value.request_id == "req-job-missing"
