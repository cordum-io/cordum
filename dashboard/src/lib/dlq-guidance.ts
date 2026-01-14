import type { DLQEntry } from "../types/api";

export type DLQGuidance = {
  title: string;
  description: string;
  action?: {
    label: string;
    href?: string;
    onClick?: string; // action identifier for handling in component
  };
  severity: "info" | "warning" | "error";
};

type ReasonCodeMapping = {
  title: string;
  description: string;
  action?: DLQGuidance["action"];
  severity: DLQGuidance["severity"];
};

const REASON_CODE_MAP: Record<string, ReasonCodeMapping> = {
  // Routing/pool issues
  no_pool_mapping: {
    title: "Topic not mapped to pool",
    description:
      "This topic has no pool routing configured. Map the topic to a worker pool in your pools configuration or pack overlays.",
    action: { label: "View Pools", href: "/system?tab=config" },
    severity: "warning",
  },
  no_workers: {
    title: "No workers available",
    description:
      "The target pool has no active workers. Check worker health or scale up the pool.",
    action: { label: "View Workers", href: "/system?tab=workers" },
    severity: "error",
  },
  pool_overloaded: {
    title: "Pool capacity exceeded",
    description:
      "All workers in the pool are busy. Consider scaling the pool or adjusting concurrency limits.",
    action: { label: "View Pool Health", href: "/system?tab=workers" },
    severity: "warning",
  },
  pool_not_found: {
    title: "Pool does not exist",
    description:
      "The configured pool for this topic does not exist. Create the pool or update the routing configuration.",
    action: { label: "View Configuration", href: "/system?tab=config" },
    severity: "error",
  },

  // Safety/policy issues
  safety_denied: {
    title: "Blocked by safety policy",
    description:
      "The Safety Kernel denied this job. Review the decision in the Policy Center to understand why and configure remediations if needed.",
    action: { label: "View Decision", onClick: "view_decision" },
    severity: "error",
  },
  approval_timeout: {
    title: "Approval timed out",
    description:
      "This job required approval but none was granted before the deadline. Re-submit the job or adjust approval timeout policies.",
    action: { label: "Policy Center", href: "/policy" },
    severity: "warning",
  },
  approval_rejected: {
    title: "Approval was rejected",
    description:
      "A reviewer explicitly rejected this job. Check the rejection reason in job details.",
    action: { label: "View Details", onClick: "view_job" },
    severity: "info",
  },

  // Retry/execution issues
  max_retries_exceeded: {
    title: "Retry limit reached",
    description:
      "The job failed after exhausting all retry attempts. Review the error, fix the underlying issue, then retry manually.",
    action: { label: "Retry Job", onClick: "retry" },
    severity: "error",
  },
  timeout: {
    title: "Job timed out",
    description:
      "Execution exceeded the deadline. Consider increasing timeout limits or optimizing the worker logic.",
    severity: "warning",
  },
  worker_crash: {
    title: "Worker crashed",
    description:
      "The worker process crashed during execution. Check worker logs for stack traces or memory issues.",
    action: { label: "View Workers", href: "/system?tab=workers" },
    severity: "error",
  },
  worker_timeout: {
    title: "Worker heartbeat lost",
    description:
      "The worker stopped responding during job execution. This may indicate a hung process or network partition.",
    action: { label: "View Workers", href: "/system?tab=workers" },
    severity: "error",
  },

  // Resource issues
  memory_exceeded: {
    title: "Memory limit exceeded",
    description:
      "The job exceeded memory limits. Reduce input size or increase worker memory allocation.",
    severity: "warning",
  },
  payload_too_large: {
    title: "Payload too large",
    description:
      "The job input or output exceeded size limits. Chunk the data or use blob storage references.",
    severity: "warning",
  },

  // Pack issues
  pack_not_installed: {
    title: "Pack not installed",
    description:
      "The job references a pack that is not installed. Install the pack or check the pack ID.",
    action: { label: "View Packs", href: "/packs" },
    severity: "error",
  },
  pack_version_mismatch: {
    title: "Pack version incompatible",
    description:
      "The job was created with a different pack version. Update the pack or re-submit with the current version.",
    action: { label: "View Packs", href: "/packs" },
    severity: "warning",
  },
  schema_validation_failed: {
    title: "Input validation failed",
    description:
      "The job input did not match the expected schema. Check the input against the pack's schema definition.",
    severity: "error",
  },

  // System issues
  nats_unavailable: {
    title: "Message broker unavailable",
    description:
      "Could not connect to NATS. Check system connectivity and NATS cluster health.",
    action: { label: "System Health", href: "/system" },
    severity: "error",
  },
  storage_unavailable: {
    title: "Storage unavailable",
    description:
      "Could not access blob storage or database. Check storage connectivity and credentials.",
    action: { label: "System Health", href: "/system" },
    severity: "error",
  },

  // Workflow issues
  workflow_cancelled: {
    title: "Parent workflow cancelled",
    description:
      "This job's parent workflow was cancelled. The job will not be retried unless the workflow is re-run.",
    severity: "info",
  },
  step_dependency_failed: {
    title: "Upstream step failed",
    description:
      "A step this job depends on failed. Fix the upstream failure first, then re-run the workflow.",
    severity: "info",
  },
};

export function getDLQGuidance(entry: DLQEntry): DLQGuidance | null {
  const code = entry.reason_code?.toLowerCase();
  if (!code) return null;

  const mapping = REASON_CODE_MAP[code];
  if (mapping) {
    return {
      title: mapping.title,
      description: mapping.description,
      action: mapping.action,
      severity: mapping.severity,
    };
  }

  // Fuzzy matching for common patterns
  if (code.includes("pool") && code.includes("no")) {
    return REASON_CODE_MAP.no_pool_mapping;
  }
  if (code.includes("worker") && (code.includes("no") || code.includes("unavailable"))) {
    return REASON_CODE_MAP.no_workers;
  }
  if (code.includes("safety") || code.includes("denied")) {
    return REASON_CODE_MAP.safety_denied;
  }
  if (code.includes("timeout") || code.includes("deadline")) {
    return REASON_CODE_MAP.timeout;
  }
  if (code.includes("retry") || code.includes("attempts")) {
    return REASON_CODE_MAP.max_retries_exceeded;
  }

  return null;
}

export function getGuidanceSeverityColor(severity: DLQGuidance["severity"]): string {
  switch (severity) {
    case "error":
      return "text-danger";
    case "warning":
      return "text-warning";
    case "info":
    default:
      return "text-info";
  }
}

export function getGuidanceSeverityBg(severity: DLQGuidance["severity"]): string {
  switch (severity) {
    case "error":
      return "bg-danger/10 border-danger/20";
    case "warning":
      return "bg-warning/10 border-warning/20";
    case "info":
    default:
      return "bg-info/10 border-info/20";
  }
}
