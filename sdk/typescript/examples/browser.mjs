import { CordumClient } from "../dist/index.mjs";

export async function runBrowserExample({
  baseUrl = "http://localhost:8080",
  auth = "dev-api-key",
  tenantId = "tenant-demo",
  jobsTarget,
  streamTarget,
} = {}) {
  const client = new CordumClient({ baseUrl, auth, tenantId });

  try {
    const jobs = await client.jobs.list();
    if (jobsTarget) {
      jobsTarget.textContent = JSON.stringify(jobs.items ?? [], null, 2);
    }

    const firstEvent = await readFirstStreamEvent(client);
    if (streamTarget) {
      streamTarget.textContent = JSON.stringify(firstEvent ?? null, null, 2);
    }

    return { jobs, firstEvent };
  } finally {
    client.close();
  }
}

async function readFirstStreamEvent(client) {
  const controller = new AbortController();

  try {
    for await (const event of client.streamEvents({ signal: controller.signal, maxReconnects: 0 })) {
      controller.abort();
      return event;
    }
  } catch (error) {
    if (!isAbortError(error)) {
      throw error;
    }
  }

  return undefined;
}

function isAbortError(error) {
  return error instanceof DOMException && error.name === "AbortError";
}
