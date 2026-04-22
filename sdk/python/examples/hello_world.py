from __future__ import annotations

import os

from cordum_sdk import CordumClient

BASE_URL = os.environ.get("CORDUM_BASE_URL", "http://localhost:8080")
API_KEY = os.environ.get("CORDUM_API_KEY", "dev-api-key")
TENANT_ID = os.environ.get("CORDUM_TENANT_ID", "tenant-dev")


def main() -> None:
    with CordumClient(BASE_URL, auth=API_KEY, tenant_id=TENANT_ID) as client:
        jobs = client.jobs.list_jobs()
        print("Found {count} jobs".format(count=len(jobs.items)))
        for job in jobs.items:
            print("- {job_id}: {state}".format(job_id=job.id, state=job.state))


if __name__ == "__main__":
    main()
