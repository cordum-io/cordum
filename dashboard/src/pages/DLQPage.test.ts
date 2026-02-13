import { beforeAll, describe, expect, it, vi } from "vitest";
import type { DLQEntry } from "../api/types";

let dlqPageInternal: {
  isOutputQuarantinedEntry: (entry: DLQEntry) => boolean;
  matchesResultFilter: (entry: DLQEntry, filterValue: string) => boolean;
};

beforeAll(async () => {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: vi.fn().mockImplementation(() => ({
      matches: false,
      media: "",
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
  dlqPageInternal = (await import("./DLQPage")).__dlqPageInternal;
});

function makeEntry(partial: Partial<DLQEntry>): DLQEntry {
  return {
    id: partial.id ?? "job-1",
    jobId: partial.jobId ?? "job-1",
    error: partial.error ?? "",
    retryCount: partial.retryCount ?? 0,
    maxRetries: partial.maxRetries ?? 0,
    originalTopic: partial.originalTopic ?? "job.test",
    failedAt: partial.failedAt ?? "",
    status: partial.status,
    reasonCode: partial.reasonCode,
    lastState: partial.lastState,
    reason: partial.reason,
    attempts: partial.attempts,
    createdAt: partial.createdAt,
  };
}

describe("DLQPage quarantine filtering", () => {
  it("detects output quarantined entries from status/reason fields", () => {
    expect(
      dlqPageInternal.isOutputQuarantinedEntry(
        makeEntry({ status: "OUTPUT_QUARANTINED" }),
      ),
    ).toBe(true);
    expect(
      dlqPageInternal.isOutputQuarantinedEntry(
        makeEntry({ reasonCode: "output_quarantined_async" }),
      ),
    ).toBe(true);
    expect(
      dlqPageInternal.isOutputQuarantinedEntry(
        makeEntry({ status: "FAILED", reasonCode: "worker_failed" }),
      ),
    ).toBe(false);
  });

  it("matches result-type filters for quarantined, denied, and failed", () => {
    const quarantined = makeEntry({ status: "OUTPUT_QUARANTINED" });
    const denied = makeEntry({ status: "DENIED" });
    const failed = makeEntry({ status: "FAILED" });

    expect(dlqPageInternal.matchesResultFilter(quarantined, "output_quarantined")).toBe(true);
    expect(dlqPageInternal.matchesResultFilter(denied, "denied")).toBe(true);
    expect(dlqPageInternal.matchesResultFilter(failed, "failed")).toBe(true);
    expect(dlqPageInternal.matchesResultFilter(quarantined, "failed")).toBe(false);
  });
});
