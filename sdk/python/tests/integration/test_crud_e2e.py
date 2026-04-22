from __future__ import annotations

from cordum_sdk import CordumClient
from cordum_sdk._generated.models.start_workflow_run_body import StartWorkflowRunBody
from cordum_sdk._generated.models.submit_job_request import SubmitJobRequest
from cordum_sdk._generated.models.update_policy_bundle_request import UpdatePolicyBundleRequest
from cordum_sdk._generated.models.workflow_definition import WorkflowDefinition


def test_crud_e2e(respx_router, gateway_state) -> None:
    del respx_router
    client = CordumClient(
        "https://api.example.test",
        auth="good-api-key",
        tenant_id="tenant-123",
    )

    created_job = client.jobs.submit_job(body=SubmitJobRequest(prompt="hello"))
    assert created_job.job_id == "job-1"

    fetched_job = client.jobs.get_job("job-1")
    assert fetched_job.id == "job-1"

    listed_jobs = client.jobs.list_jobs()
    assert [item.id for item in listed_jobs.items] == ["job-1"]

    canceled_job = client.jobs.cancel_job("job-1")
    assert canceled_job.state == "canceled"

    workflow = WorkflowDefinition.from_dict(
        {
            "id": "wf-1",
            "name": "Workflow 1",
            "steps": {"step-1": {"id": "step-1", "type": "job", "name": "Execute"}},
        }
    )
    created_workflow = client.workflows.create_workflow(body=workflow)
    assert created_workflow.id == "wf-1"

    workflows = client.workflows.list_workflows()
    assert [item.id for item in workflows] == ["wf-1"]

    fetched_workflow = client.workflows.get_workflow("wf-1")
    assert fetched_workflow.id == "wf-1"

    run = client.workflows.start_workflow_run(
        "wf-1",
        body=StartWorkflowRunBody.from_dict({"foo": "bar"}),
    )
    assert run.run_id == "run-1"

    fetched_run = client.workflows.get_workflow_run("run-1")
    assert fetched_run.id == "run-1"

    bundle = client.policies.update_policy_bundle(
        "bundle-1",
        body=UpdatePolicyBundleRequest(
            content="rules: []",
            enabled=True,
            author="sdk",
            message="apply",
        ),
    )
    assert bundle.id == "bundle-1"

    fetched_bundle = client.policies.get_policy_bundle("bundle-1")
    assert fetched_bundle.id == "bundle-1"

    bundles = client.policies.list_policy_bundles()
    assert bundles.items[0].id == "bundle-1"

    workers = client.workers.list_workers()
    assert workers.items[0].worker_id == "worker-1"

    worker = client.workers.get_worker("worker-1")
    assert worker.worker_id == "worker-1"

    agent = client.agents.get_worker("worker-1")
    assert agent.worker_id == "worker-1"

    worker_jobs = client.workers.get_worker_jobs("worker-1")
    assert worker_jobs.items[0].id == "job-1"

    mcp_status = client.mcp.mcp_status()
    assert mcp_status["connected"] is True

    client.workflows.delete_workflow("wf-1")
    client.policies.delete_policy_bundle("bundle-1")
    client.close()

    assert gateway_state.request_log[:8] == [
        ("POST", "/api/v1/jobs"),
        ("GET", "/api/v1/jobs/job-1"),
        ("GET", "/api/v1/jobs"),
        ("POST", "/api/v1/jobs/job-1/cancel"),
        ("POST", "/api/v1/workflows"),
        ("GET", "/api/v1/workflows"),
        ("GET", "/api/v1/workflows/wf-1"),
        ("POST", "/api/v1/workflows/wf-1/runs"),
    ]
