import type { Job } from "@/api/types";

export interface JobParentRefs {
  runId: string | undefined;
  sessionId: string | undefined;
  workflowId: string | undefined;
}

// Single source of truth for "is this job a Run / Session / Workflow child?".
// Drift between OriginPill, ParentContextBanner, MetadataBar, and the
// empty-context-card gate caused inconsistent navigation affordances —
// see task-dc086833. Always go through this helper.
export function getJobParentRefs(job: Job): JobParentRefs {
  const runId =
    job.workflowRunId ||
    (job.metadata?.run_id as string | undefined) ||
    (job.labels?.run_id as string | undefined) ||
    undefined;
  const sessionId =
    (job.metadata?.session_id as string | undefined) ||
    (job.labels?.session_id as string | undefined) ||
    undefined;
  const workflowId = job.workflowId || undefined;
  return { runId, sessionId, workflowId };
}
