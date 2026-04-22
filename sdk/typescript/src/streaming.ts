import { raiseForStatus } from "./errors.js";

export type KnownStreamEventName =
  | "job.status_changed"
  | "worker.heartbeat"
  | "audit.entry_created"
  | "workflow.run_event"
  | "policy.decision";

type KnownEventPayload = Record<string, unknown>;

export type KnownStreamEvent =
  | { event: "job.status_changed"; data: KnownEventPayload; id?: string; retry?: number }
  | { event: "worker.heartbeat"; data: KnownEventPayload; id?: string; retry?: number }
  | { event: "audit.entry_created"; data: KnownEventPayload; id?: string; retry?: number }
  | { event: "workflow.run_event"; data: KnownEventPayload; id?: string; retry?: number }
  | { event: "policy.decision"; data: KnownEventPayload; id?: string; retry?: number };

export type StreamEvent =
  | KnownStreamEvent
  | { event: string; data: unknown; id?: string; retry?: number };

export interface StreamEventsOptions {
  baseUrl: string;
  fetch?: typeof globalThis.fetch;
  signal?: AbortSignal;
  rootSignal?: AbortSignal;
  maxReconnects?: number;
  initialReconnectDelayMs?: number;
}

export async function* streamEvents(options: StreamEventsOptions): AsyncGenerator<StreamEvent> {
  const fetchImpl = options.fetch ?? globalThis.fetch;
  const controller = new AbortController();
  const cleanup = bindAbortSignals(controller, options.signal, options.rootSignal);
  const maxReconnects = options.maxReconnects ?? 5;
  let reconnectCount = 0;
  let retryDelayMs = options.initialReconnectDelayMs ?? 500;
  let lastEventId: string | undefined;

  try {
    while (true) {
      throwIfAborted(controller.signal);

      const response = await fetchImpl(new URL("/api/v1/stream", options.baseUrl), {
        headers: {
          Accept: "text/event-stream",
          ...(lastEventId ? { "Last-Event-ID": lastEventId } : {}),
        },
        signal: controller.signal,
      });

      if (!response.ok) {
        await raiseForStatus(response);
      }
      if (!response.body) {
        throw new Error("SSE response body missing");
      }

      for await (const event of parseEventStream(response.body, controller.signal)) {
        if (event.id) {
          lastEventId = event.id;
        }
        if (event.retry !== undefined) {
          retryDelayMs = event.retry;
        }
        yield event;
      }

      if (controller.signal.aborted || reconnectCount >= maxReconnects) {
        return;
      }

      reconnectCount += 1;
      await sleep(computeReconnectDelay(reconnectCount, retryDelayMs), controller.signal);
    }
  } finally {
    cleanup();
    controller.abort(new DOMException("Stream closed", "AbortError"));
  }
}

async function* parseEventStream(body: ReadableStream<Uint8Array>, signal: AbortSignal): AsyncGenerator<StreamEvent> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  try {
    while (true) {
      throwIfAborted(signal);
      const { done, value } = await reader.read();
      if (done) {
        buffer += decoder.decode();
        if (buffer.trim()) {
          const event = parseFrame(buffer);
          if (event) {
            yield event;
          }
        }
        return;
      }

      buffer += decoder.decode(value, { stream: true }).replace(/\r\n/g, "\n");

      let boundaryIndex = buffer.indexOf("\n\n");
      while (boundaryIndex !== -1) {
        const frame = buffer.slice(0, boundaryIndex);
        buffer = buffer.slice(boundaryIndex + 2);
        const event = parseFrame(frame);
        if (event) {
          yield event;
        }
        boundaryIndex = buffer.indexOf("\n\n");
      }
    }
  } finally {
    await reader.cancel().catch(() => undefined);
  }
}

function parseFrame(frame: string): StreamEvent | undefined {
  if (!frame.trim()) {
    return undefined;
  }

  let eventName = "message";
  let eventId: string | undefined;
  let retry: number | undefined;
  const dataLines: string[] = [];

  for (const rawLine of frame.split("\n")) {
    if (!rawLine || rawLine.startsWith(":")) {
      continue;
    }

    const separatorIndex = rawLine.indexOf(":");
    const field = separatorIndex === -1 ? rawLine : rawLine.slice(0, separatorIndex);
    const value = separatorIndex === -1 ? "" : rawLine.slice(separatorIndex + 1).replace(/^ /, "");

    switch (field) {
      case "event":
        eventName = value || "message";
        break;
      case "data":
        dataLines.push(value);
        break;
      case "id":
        eventId = value;
        break;
      case "retry": {
        const parsed = Number(value);
        if (Number.isFinite(parsed)) {
          retry = parsed;
        }
        break;
      }
      default:
        break;
    }
  }

  const rawData = dataLines.join("\n");
  const data = rawData ? parseEventData(rawData) : undefined;
  return coerceEvent(eventName, data, eventId, retry);
}

function parseEventData(rawData: string): unknown {
  try {
    return JSON.parse(rawData);
  } catch {
    return rawData;
  }
}

function coerceEvent(event: string, data: unknown, id?: string, retry?: number): StreamEvent {
  switch (event) {
    case "job.status_changed":
    case "worker.heartbeat":
    case "audit.entry_created":
    case "workflow.run_event":
    case "policy.decision":
      return { event, data: asRecord(data), id, retry } as KnownStreamEvent;
    default:
      return {
        event,
        data,
        ...(id !== undefined ? { id } : {}),
        ...(retry !== undefined ? { retry } : {}),
      };
  }
}

function asRecord(data: unknown): KnownEventPayload {
  return typeof data === "object" && data !== null ? (data as KnownEventPayload) : { value: data };
}

function computeReconnectDelay(reconnectCount: number, baseDelayMs: number): number {
  return Math.min(30_000, Math.max(0, baseDelayMs * 2 ** Math.max(0, reconnectCount - 1)));
}

function bindAbortSignals(
  controller: AbortController,
  ...signals: Array<AbortSignal | undefined>
): () => void {
  const listeners: Array<{ signal: AbortSignal; listener: () => void }> = [];

  const abortWith = (signal: AbortSignal) => {
    cleanup();
    controller.abort(signal.reason);
  };

  for (const signal of signals) {
    if (!signal) {
      continue;
    }
    if (signal.aborted) {
      abortWith(signal);
      return cleanup;
    }
    const listener = () => abortWith(signal);
    listeners.push({ signal, listener });
    signal.addEventListener("abort", listener, { once: true });
  }

  function cleanup() {
    for (const { signal, listener } of listeners) {
      signal.removeEventListener("abort", listener);
    }
  }

  return cleanup;
}

function throwIfAborted(signal: AbortSignal): void {
  if (signal.aborted) {
    throw signal.reason ?? new DOMException("The operation was aborted.", "AbortError");
  }
}

function sleep(delayMs: number, signal: AbortSignal): Promise<void> {
  if (delayMs <= 0) {
    throwIfAborted(signal);
    return Promise.resolve();
  }

  return new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => {
      cleanup();
      resolve();
    }, delayMs);

    const onAbort = () => {
      clearTimeout(timeout);
      cleanup();
      reject(signal.reason ?? new DOMException("The operation was aborted.", "AbortError"));
    };

    const cleanup = () => {
      signal.removeEventListener("abort", onAbort);
    };

    signal.addEventListener("abort", onAbort, { once: true });
  });
}
