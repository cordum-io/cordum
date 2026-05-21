import React, { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SubmitJobDialog, matchesJobSearch } from "./JobsPage";
import type { Job } from "@/api/types";

const { hookState } = vi.hoisted(() => ({
  hookState: {
    submitJob: {
      mutate: vi.fn(),
      isPending: false,
    },
  },
}));

vi.mock("@/hooks/useJobs", () => ({
  useSubmitJob: () => hookState.submitJob,
}));

// `readStoredJobsPageSize` was deleted in the Phase 3 wk4 rewrite (task-2c3c8a04):
// pagination state migrated from local-storage page-size + URL ?page= to
// primitives/DataTable virtualization (DOM-node count stays bounded regardless
// of row count) + nuqs-driven URL filter state.

describe("SubmitJobDialog accessibility", () => {
  beforeEach(() => {
    hookState.submitJob = {
      mutate: vi.fn(),
      isPending: false,
    };
  });

  it("focuses the topic field on open and restores focus to the trigger on close", () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);

    function Harness() {
      const [open, setOpen] = useState(false);
      return React.createElement(
        MemoryRouter,
        { initialEntries: ["/jobs"] },
        React.createElement(
          React.Fragment,
          null,
          React.createElement(
            "button",
            { type: "button", onClick: () => setOpen(true) },
            "Open submit dialog",
          ),
          React.createElement(SubmitJobDialog, {
            open,
            onClose: () => setOpen(false),
          }),
        ),
      );
    }

    try {
      act(() => {
        root.render(React.createElement(Harness));
      });

      const trigger = Array.from(container.querySelectorAll("button")).find(
        (button) => button.textContent?.includes("Open submit dialog"),
      ) as HTMLButtonElement | undefined;

      expect(trigger).toBeDefined();

      act(() => {
        trigger?.focus();
        trigger?.click();
      });

      const topicInput = container.querySelector(
        'input[aria-label="Job topic"]',
      ) as HTMLInputElement | null;
      expect(topicInput).not.toBeNull();
      expect(document.activeElement).toBe(topicInput);

      const closeButton = container.querySelector(
        'button[aria-label="Close submit job dialog"]',
      ) as HTMLButtonElement | null;
      expect(closeButton).not.toBeNull();

      act(() => {
        closeButton?.click();
      });

      expect(document.activeElement).toBe(trigger);
    } finally {
      act(() => root.unmount());
      container.remove();
    }
  });
});

// task-cafacca3 reopen #1 — JobsPage search predicate now covers topic /
// pool / tenant / session in addition to id / trace / run. The predicate is
// exported as `matchesJobSearch` so it can be exercised directly without a
// full-page render. These tests guard against regressions to the new arms;
// dropping any one would surface as a missing match below.
function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: "job-xyz",
    topic: "job.code-review",
    status: "running",
    type: "job.code-review",
    pool: "default",
    capabilities: [],
    riskTags: [],
    metadata: {},
    createdAt: "2026-05-17T00:00:00.000Z",
    updatedAt: "2026-05-17T00:00:00.000Z",
    labels: {},
    ...overrides,
  } as Job;
}

describe("JobsPage main search predicate (matchesJobSearch)", () => {
  it("Test_searchInput_matchesTopic: matches by job topic substring (case-insensitive)", () => {
    const job = makeJob({ topic: "job.code-review", id: "j-001" });
    expect(matchesJobSearch(job, "code-review")).toBe(true);
    expect(matchesJobSearch(job, "CODE")).toBe(true);
    expect(matchesJobSearch(job, "code-r")).toBe(true);
    expect(matchesJobSearch(job, "nomatch")).toBe(false);
  });

  it("Test_searchInput_matchesPool: matches by pool substring (new arm)", () => {
    const job = makeJob({ pool: "pool-fraud-a", topic: "unrelated.topic", id: "j-002" });
    expect(matchesJobSearch(job, "pool-fraud")).toBe(true);
    expect(matchesJobSearch(job, "FRAUD")).toBe(true);
    expect(matchesJobSearch(job, "pool-z")).toBe(false);
  });

  it("Test_searchInput_matchesSessionID: matches by parent sessionId (new arm)", () => {
    const job = makeJob({
      id: "j-003",
      topic: "unrelated",
      metadata: { session_id: "sess-abc-123" },
    });
    expect(matchesJobSearch(job, "sess-abc")).toBe(true);
    expect(matchesJobSearch(job, "SESS-ABC-123")).toBe(true);
    expect(matchesJobSearch(job, "sess-xyz")).toBe(false);
  });

  it("matches by tenant substring (new arm)", () => {
    const job = makeJob({ tenant: "tenant-prod", id: "j-004", topic: "unrelated" });
    expect(matchesJobSearch(job, "tenant-prod")).toBe(true);
    expect(matchesJobSearch(job, "TENANT")).toBe(true);
    expect(matchesJobSearch(job, "tenant-staging")).toBe(false);
  });

  it("preserves the legacy arms (id / traceId / workflowRunId)", () => {
    const job = makeJob({
      id: "job-zzz",
      traceId: "trace-deadbeef",
      workflowRunId: "wfr-001",
      topic: "unrelated",
    });
    expect(matchesJobSearch(job, "job-zz")).toBe(true);
    expect(matchesJobSearch(job, "deadbeef")).toBe(true);
    expect(matchesJobSearch(job, "wfr-001")).toBe(true);
  });

  it("treats blank queries as a match-all (no filter applied)", () => {
    const job = makeJob({ topic: "x", id: "y" });
    expect(matchesJobSearch(job, "")).toBe(true);
    expect(matchesJobSearch(job, "   ")).toBe(true);
  });

  it("returns false when none of the searchable fields contains the query", () => {
    const job = makeJob({
      id: "job-1",
      topic: "topic-a",
      pool: "pool-a",
      tenant: "tenant-a",
      traceId: "trace-a",
      workflowRunId: "wfr-a",
      metadata: { session_id: "sess-a" },
    });
    expect(matchesJobSearch(job, "zzz")).toBe(false);
  });
});
