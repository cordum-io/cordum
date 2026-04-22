import { CordumClient } from "@cordum/sdk";
import { worker } from "./mocks/browser";

const jobsNode = document.querySelector<HTMLPreElement>("#jobs");
const streamNode = document.querySelector<HTMLPreElement>("#stream");

async function main(): Promise<void> {
  await worker.start({
    onUnhandledRequest: "error",
    serviceWorker: {
      url: "/mockServiceWorker.js",
    },
  });

  const client = new CordumClient({
    baseUrl: window.location.origin,
    auth: "browser-key",
    tenantId: "tenant-a",
  });

  const jobs = await client.jobs.list();
  if (jobsNode) {
    jobsNode.textContent = JSON.stringify(jobs, null, 2);
  }

  const events: Array<{ event: string; data: unknown }> = [];
  for await (const event of client.streamEvents({ maxReconnects: 0 })) {
    events.push({ event: event.event, data: event.data });
  }

  if (streamNode) {
    streamNode.textContent = JSON.stringify(events, null, 2);
  }
}

void main().catch((error) => {
  if (jobsNode) {
    jobsNode.textContent = `error: ${String(error)}`;
  }
  if (streamNode) {
    streamNode.textContent = `error: ${String(error)}`;
  }
});
