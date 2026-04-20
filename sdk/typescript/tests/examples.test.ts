import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import { setupServer } from "msw/node";
import { HttpResponse, http } from "msw";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { runNodeExample } from "../examples/node.js";

const encoder = new TextEncoder();
const server = setupServer(
  http.get("http://localhost:8080/api/v1/jobs", ({ request }) => {
    expect(request.headers.get("x-api-key")).toBe("dev-api-key");
    expect(request.headers.get("x-tenant-id")).toBe("tenant-demo");

    return HttpResponse.json(
      {
        items: [{ id: "job-example-1", state: "queued" }],
        next_cursor: null,
      },
      {
        headers: {
          "X-Request-Id": "req-example-jobs",
        },
      },
    );
  }),
  http.get("http://localhost:8080/api/v1/stream", () => {
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(
          encoder.encode('event: workflow.run_event\ndata: {"run_id":"run-example-1","status":"running"}\n\n'),
        );
        controller.close();
      },
    });

    return new HttpResponse(stream, {
      status: 200,
      headers: {
        "content-type": "text/event-stream",
      },
    });
  }),
);

beforeAll(() => {
  server.listen({ onUnhandledRequest: "error" });
});

afterEach(() => {
  server.resetHandlers();
});

afterAll(() => {
  server.close();
});

describe("examples", () => {
  it("runs the Node example against mocked localhost handlers", async () => {
    const result = await runNodeExample();

    expect(result.jobs.items).toEqual([{ id: "job-example-1", state: "queued" }]);
    expect(result.firstEvent).toMatchObject({
      event: "workflow.run_event",
      data: { run_id: "run-example-1", status: "running" },
    });
  });

  it("runs the browser example module against mocked localhost handlers", async () => {
    // @ts-expect-error browser example is a plain `.mjs` file consumed directly by the HTML example.
    const { runBrowserExample } = await import("../examples/browser.mjs");
    const jobsTarget = { textContent: null };
    const streamTarget = { textContent: null };

    const result = await runBrowserExample({ jobsTarget, streamTarget });

    expect(result.jobs.items).toEqual([{ id: "job-example-1", state: "queued" }]);
    expect(result.firstEvent).toMatchObject({
      event: "workflow.run_event",
      data: { run_id: "run-example-1", status: "running" },
    });
    expect(jobsTarget.textContent).toContain("job-example-1");
    expect(streamTarget.textContent).toContain("workflow.run_event");
  });

  it("keeps the browser HTML wired to the local module and localhost API", async () => {
    const html = await readFile(resolve("examples/browser.html"), "utf8");

    expect(html).toContain("./browser.mjs");
    expect(html).toContain("http://localhost:8080");
  });
});
