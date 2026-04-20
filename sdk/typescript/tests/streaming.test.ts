import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { streamEvents } from "../src/streaming.js";

describe("streaming helpers", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("parses SSE frames including multi-line data", async () => {
    const fetchMock = vi.fn(async () => createStreamResponse([
      'id: evt-1\nevent: job.status_changed\ndata: {"jobId":"job-1",\ndata: "status":"running"}\nretry: 25\n\n',
      'event: audit.entry_created\ndata: {"id":"audit-1"}\n\n',
    ]));

    const events = await collect(streamEvents({
      baseUrl: "https://cordum.test",
      fetch: fetchMock,
      maxReconnects: 0,
    }));

    expect(events).toEqual([
      {
        id: "evt-1",
        event: "job.status_changed",
        data: { jobId: "job-1", status: "running" },
        retry: 25,
      },
      {
        event: "audit.entry_created",
        data: { id: "audit-1" },
        id: undefined,
        retry: undefined,
      },
    ]);
  });

  it("reconnects after a disconnect and resumes streaming", async () => {
    const fetchMock = vi
      .fn(async () => createStreamResponse(['event: job.status_changed\ndata: {"jobId":"job-1"}\nretry: 10\n\n']))
      .mockImplementationOnce(async () => createStreamResponse(['event: job.status_changed\ndata: {"jobId":"job-1"}\nretry: 10\n\n']))
      .mockImplementationOnce(async () => createStreamResponse(['event: worker.heartbeat\ndata: {"workerId":"worker-1"}\n\n']));

    const promise = collect(streamEvents({
      baseUrl: "https://cordum.test",
      fetch: fetchMock,
      maxReconnects: 1,
      initialReconnectDelayMs: 10,
    }));

    await vi.advanceTimersByTimeAsync(10);
    const events = await promise;

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(events.map((event) => event.event)).toEqual(["job.status_changed", "worker.heartbeat"]);
  });

  it("aborts mid-stream and cleans up readers", async () => {
    const abortController = new AbortController();
    let cancelled = false;
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const stream = new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(new TextEncoder().encode('event: policy.decision\ndata: {"decision":"allow"}\n\n'));
          init?.signal?.addEventListener("abort", () => {
            cancelled = true;
            controller.error(new DOMException("Aborted", "AbortError"));
          });
        },
        cancel() {
          cancelled = true;
        },
      });

      return new Response(stream, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      });
    });

    const iterator = streamEvents({
      baseUrl: "https://cordum.test",
      fetch: fetchMock,
      signal: abortController.signal,
      maxReconnects: 0,
    });

    const first = await iterator.next();
    expect(first.value).toMatchObject({ event: "policy.decision" });

    abortController.abort(new DOMException("Stopped", "AbortError"));
    await expect(iterator.next()).rejects.toMatchObject({ name: "AbortError" });
    expect(cancelled).toBe(true);
  });
});

async function collect<T>(iterator: AsyncIterable<T>): Promise<T[]> {
  const items: T[] = [];
  for await (const item of iterator) {
    items.push(item);
  }
  return items;
}

function createStreamResponse(frames: string[]): Response {
  const encoder = new TextEncoder();
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const frame of frames) {
        controller.enqueue(encoder.encode(frame));
      }
      controller.close();
    },
  });

  return new Response(stream, {
    status: 200,
    headers: { "content-type": "text/event-stream" },
  });
}
