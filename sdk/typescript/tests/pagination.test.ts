import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { CordumClient } from "../src/client.js";
import { getNextCursor, paginate } from "../src/pagination.js";

const server = setupServer();

beforeAll(() => {
  server.listen({ onUnhandledRequest: "error" });
});

afterEach(() => {
  server.resetHandlers();
});

afterAll(() => {
  server.close();
});

describe("pagination helpers", () => {
  it("yields every item across multiple pages exactly once", async () => {
    const items = await collect(
      paginate<{ id: string }, { items: { id: string }[]; next_cursor: string | null }>(
        async () => ({ items: [{ id: "job-1" }, { id: "job-2" }], next_cursor: "cursor-2" }),
        getNextCursor,
        async (cursor) => {
          expect(cursor).toBe("cursor-2");
          return { items: [{ id: "job-3" }], next_cursor: null };
        },
      ),
    );

    expect(items).toEqual([{ id: "job-1" }, { id: "job-2" }, { id: "job-3" }]);
  });

  it("aborts pagination when the consumer breaks early", async () => {
    let capturedSignal: AbortSignal | undefined;

    const iterator = paginate<number, { items: number[]; next_cursor: string | null }>(
      async (signal) => {
        capturedSignal = signal;
        return { items: [1, 2], next_cursor: "next-page" };
      },
      getNextCursor,
      async () => ({ items: [3], next_cursor: null }),
    );

    const collected: number[] = [];
    for await (const item of iterator) {
      collected.push(item);
      break;
    }

    expect(collected).toEqual([1]);
    expect(capturedSignal?.aborted).toBe(true);
  });

  it("adds typed paginators to client.jobs and client.workflows", async () => {
    server.use(
      http.get("https://cordum.test/api/v1/jobs", ({ request }) => {
        const cursor = new URL(request.url).searchParams.get("cursor");
        if (!cursor) {
          return HttpResponse.json({ items: [{ id: "job-1" }], next_cursor: "cursor-2" });
        }
        return HttpResponse.json({ items: [{ id: "job-2" }], next_cursor: null });
      }),
      http.get("https://cordum.test/api/v1/workflows", () => HttpResponse.json([{ id: "wf-1" }, { id: "wf-2" }])),
    );

    const client = new CordumClient({
      baseUrl: "https://cordum.test",
      auth: "sdk-key",
    });

    const jobIds: string[] = [];
    for await (const job of client.jobs.paginate()) {
      jobIds.push((job as { id?: string }).id ?? "");
    }

    const workflowIds: string[] = [];
    for await (const workflow of client.workflows.paginate()) {
      workflowIds.push((workflow as { id?: string }).id ?? "");
    }

    expect(jobIds).toEqual(["job-1", "job-2"]);
    expect(workflowIds).toEqual(["wf-1", "wf-2"]);
  });
});

async function collect<T>(iterator: AsyncIterable<T>): Promise<T[]> {
  const items: T[] = [];
  for await (const item of iterator) {
    items.push(item);
  }
  return items;
}
