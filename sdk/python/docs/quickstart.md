# Cordum Python SDK quickstart

This end-to-end example shows the typical flow:

1. create a workflow definition
2. start a workflow run
3. watch the event bus
4. inspect final run state

```python
import asyncio

from cordum_sdk import AsyncCordumClient
from cordum_sdk._generated.models.start_workflow_run_body import StartWorkflowRunBody
from cordum_sdk._generated.models.workflow_definition import WorkflowDefinition


async def main() -> None:
    workflow = WorkflowDefinition.from_dict(
        {
            "id": "hello-workflow",
            "name": "Hello workflow",
            "steps": {
                "step-1": {
                    "id": "step-1",
                    "type": "job",
                    "name": "Execute hello job",
                }
            },
        }
    )

    async with AsyncCordumClient(
        "https://api.cordum.example",
        auth="cordum_api_key",
        tenant_id="tenant-123",
    ) as client:
        await client.workflows.create_workflow(body=workflow)
        run = await client.workflows.start_workflow_run(
            "hello-workflow",
            body=StartWorkflowRunBody.from_dict({"prompt": "say hello"}),
        )

        async for event in client.mcp.stream():
            payload = event.data if isinstance(event.data, dict) else {}
            if payload.get("run_id") == run.run_id:
                print("event:", event.event, payload)
            if payload.get("run_id") == run.run_id and payload.get("status") in {"completed", "failed"}:
                break

        detail = await client.workflows.get_workflow_run(run.run_id)
        print("final status:", detail.status)


asyncio.run(main())
```

## Notes

- `client.workflows` merges workflow-definition and workflow-run operations into
  one ergonomic namespace.
- Use `client.jobs.paginate("list_jobs")` when an endpoint exposes cursor-based
  pagination.
- Use an `Idempotency-Key` for retried `POST` calls that create server-side
  work.
- The vendored generated models live under `cordum_sdk._generated.models`.
