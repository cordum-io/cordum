import { http, HttpResponse } from "msw";

const encoder = new TextEncoder();

export const handlers = [
  http.get("/api/v1/jobs", () =>
    HttpResponse.json(
      {
        items: [{ id: "job-browser-1", state: "queued" }],
        next_cursor: null,
      },
      {
        headers: {
          "X-Request-Id": "req-browser-jobs",
        },
      },
    )),
  http.get("/api/v1/stream", () => {
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(
          encoder.encode('event: job.status_changed\ndata: {"id":"job-browser-1","state":"running"}\n\n'),
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
];
