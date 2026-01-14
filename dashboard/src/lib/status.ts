import {
  AlertOctagon,
  AlertTriangle,
  CheckCircle2,
  CircleDot,
  Clock,
  PauseCircle,
  PlayCircle,
  ShieldAlert,
  ShieldCheck,
  Slash,
  TimerOff,
} from "lucide-react";

export type StatusTone = "success" | "warning" | "danger" | "info" | "muted" | "accent";
export type StatusShape = "circle" | "diamond" | "square" | "shield" | "triangle";

export type StatusMeta = {
  label: string;
  tone: StatusTone;
  shape: StatusShape;
  icon: typeof PlayCircle;
};

const runStatusMap: Record<string, StatusMeta> = {
  pending: { label: "Pending", tone: "muted", shape: "circle", icon: CircleDot },
  running: { label: "Running", tone: "success", shape: "circle", icon: PlayCircle },
  waiting: { label: "Waiting", tone: "warning", shape: "square", icon: Clock },
  succeeded: { label: "Succeeded", tone: "success", shape: "circle", icon: CheckCircle2 },
  failed: { label: "Failed", tone: "danger", shape: "diamond", icon: AlertOctagon },
  cancelled: { label: "Cancelled", tone: "muted", shape: "diamond", icon: Slash },
  timed_out: { label: "Timed Out", tone: "danger", shape: "square", icon: TimerOff },
};

const jobStatusMap: Record<string, StatusMeta> = {
  PENDING: { label: "Pending", tone: "muted", shape: "circle", icon: CircleDot },
  APPROVAL_REQUIRED: { label: "Approval", tone: "warning", shape: "shield", icon: ShieldAlert },
  SCHEDULED: { label: "Scheduled", tone: "info", shape: "square", icon: Clock },
  DISPATCHED: { label: "Dispatched", tone: "info", shape: "square", icon: PlayCircle },
  RUNNING: { label: "Running", tone: "success", shape: "circle", icon: PlayCircle },
  SUCCEEDED: { label: "Succeeded", tone: "success", shape: "circle", icon: CheckCircle2 },
  FAILED: { label: "Failed", tone: "danger", shape: "diamond", icon: AlertOctagon },
  CANCELLED: { label: "Cancelled", tone: "muted", shape: "diamond", icon: Slash },
  TIMEOUT: { label: "Timeout", tone: "danger", shape: "square", icon: TimerOff },
  DENIED: { label: "Denied", tone: "danger", shape: "triangle", icon: AlertTriangle },
};

export function runStatusMeta(status?: string): StatusMeta {
  if (!status) {
    return { label: "Unknown", tone: "muted", shape: "square", icon: CircleDot };
  }
  return runStatusMap[status] || {
    label: status,
    tone: "muted",
    shape: "square",
    icon: PauseCircle,
  };
}

export function jobStatusMeta(state?: string): StatusMeta {
  if (!state) {
    return { label: "Unknown", tone: "muted", shape: "square", icon: PauseCircle };
  }
  return jobStatusMap[state] || {
    label: state,
    tone: "muted",
    shape: "square",
    icon: PauseCircle,
  };
}

export function approvalStatusMeta(isRequired?: boolean): StatusMeta {
  if (isRequired) {
    return { label: "Awaiting Approval", tone: "warning", shape: "shield", icon: ShieldAlert };
  }
  return { label: "Approval Granted", tone: "success", shape: "shield", icon: ShieldCheck };
}

export type DecisionType = "allow" | "deny" | "require_approval" | "throttle" | "allow_with_constraints" | "unknown";

export function decisionTypeMeta(decision?: string): { label: string; shortLabel: string; type: DecisionType; tone: StatusTone } {
  const normalized = (decision || "").toUpperCase().replace("DECISION_TYPE_", "");
  if (!normalized) {
    return { label: "Unknown", shortLabel: "?", type: "unknown", tone: "muted" };
  }
  if (normalized.includes("DENY")) {
    return { label: "Denied", shortLabel: "DENY", type: "deny", tone: "danger" };
  }
  if (normalized.includes("REQUIRE_APPROVAL") || normalized.includes("REQUIRE-APPROVAL")) {
    return { label: "Requires Approval", shortLabel: "APPROVAL", type: "require_approval", tone: "warning" };
  }
  if (normalized.includes("THROTTLE")) {
    return { label: "Throttled", shortLabel: "THROTTLE", type: "throttle", tone: "warning" };
  }
  if (normalized.includes("ALLOW_WITH") || normalized.includes("ALLOW-WITH")) {
    return { label: "Allowed with Constraints", shortLabel: "CONSTRAINED", type: "allow_with_constraints", tone: "info" };
  }
  if (normalized.includes("ALLOW")) {
    return { label: "Allowed", shortLabel: "ALLOW", type: "allow", tone: "success" };
  }
  return { label: normalized, shortLabel: normalized.slice(0, 10), type: "unknown", tone: "muted" };
}
