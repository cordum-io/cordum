import { CordumClient, type StreamEvent } from "@cordum/sdk";

export interface NodeExampleOptions {
  baseUrl?: string;
  auth?: string;
  tenantId?: string;
}

export interface NodeExampleResult {
  jobs: Awaited<ReturnType<CordumClient["jobs"]["list"]>>;
  firstEvent: StreamEvent | undefined;
}

export async function runNodeExample(options: NodeExampleOptions = {}): Promise<NodeExampleResult> {
  const client = new CordumClient({
    baseUrl: options.baseUrl ?? "http://localhost:8080",
    auth: options.auth ?? "dev-api-key",
    tenantId: options.tenantId ?? "tenant-demo",
  });

  try {
    const jobs = await client.jobs.list();
    const firstEvent = await readFirstStreamEvent(client);
    return { jobs, firstEvent };
  } finally {
    client.close();
  }
}

async function readFirstStreamEvent(client: CordumClient): Promise<StreamEvent | undefined> {
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

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}
