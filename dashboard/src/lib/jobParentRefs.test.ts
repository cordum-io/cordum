import { describe, expect, it } from "vitest";
import { getJobParentRefs } from "./jobParentRefs";
import type { Job } from "@/api/types";

function makeJob(partial: Partial<Job>): Job {
  return {
    id: "job-1",
    type: "job",
    topic: "job.test",
    status: "pending",
    pool: "default",
    capabilities: [],
    riskTags: [],
    metadata: {},
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    ...partial,
  };
}

describe("getJobParentRefs", () => {
  it("returns all-undefined for an empty job", () => {
    const refs = getJobParentRefs(makeJob({}));
    expect(refs).toEqual({
      runId: undefined,
      sessionId: undefined,
      workflowId: undefined,
    });
  });

  it("reads runId from job.workflowRunId when set", () => {
    const refs = getJobParentRefs(makeJob({ workflowRunId: "wfr-top" }));
    expect(refs.runId).toBe("wfr-top");
  });

  it("falls back to metadata.run_id when workflowRunId is absent", () => {
    const refs = getJobParentRefs(makeJob({ metadata: { run_id: "wfr-meta" } }));
    expect(refs.runId).toBe("wfr-meta");
  });

  it("falls back to labels.run_id when both top-level and metadata are absent", () => {
    const refs = getJobParentRefs(makeJob({ labels: { run_id: "wfr-label" } }));
    expect(refs.runId).toBe("wfr-label");
  });

  it("honors precedence: workflowRunId > metadata.run_id > labels.run_id", () => {
    const allThree = getJobParentRefs(
      makeJob({
        workflowRunId: "wfr-top",
        metadata: { run_id: "wfr-meta" },
        labels: { run_id: "wfr-label" },
      }),
    );
    expect(allThree.runId).toBe("wfr-top");

    const metaWinsOverLabel = getJobParentRefs(
      makeJob({
        metadata: { run_id: "wfr-meta" },
        labels: { run_id: "wfr-label" },
      }),
    );
    expect(metaWinsOverLabel.runId).toBe("wfr-meta");
  });

  it("resolves sessionId from metadata > labels and workflowId from top-level only", () => {
    const sessionFromMetadata = getJobParentRefs(
      makeJob({
        metadata: { session_id: "sess-meta" },
        labels: { session_id: "sess-label" },
      }),
    );
    expect(sessionFromMetadata.sessionId).toBe("sess-meta");

    const sessionFromLabels = getJobParentRefs(
      makeJob({ labels: { session_id: "sess-label" } }),
    );
    expect(sessionFromLabels.sessionId).toBe("sess-label");

    const workflowOnlyTopLevel = getJobParentRefs(
      makeJob({
        workflowId: "wf-top",
        metadata: { workflow_id: "wf-meta" },
        labels: { workflow_id: "wf-label" },
      }),
    );
    expect(workflowOnlyTopLevel).toEqual({
      runId: undefined,
      sessionId: undefined,
      workflowId: "wf-top",
    });
  });
});
