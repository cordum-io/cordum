import type { BadgeColorVariant } from "@/components/ui/Badge";

/**
 * Worker / pool / agent connection-status badge color.
 *   online | active   -> success
 *   draining          -> warning
 *   offline | error   -> danger
 *   unknown           -> default
 */
export function workerStatusVariant(
  status: string | undefined | null,
): BadgeColorVariant {
  switch (status) {
    case "online":
    case "active":
      return "success";
    case "draining":
      return "warning";
    case "offline":
    case "error":
      return "danger";
    default:
      return "default";
  }
}

/**
 * Job lifecycle badge color (mirrors the scheduler job-state taxonomy in
 * core/controlplane/safetykernel/scanners.go and dashboard/src/api/types.ts).
 */
export function jobStatusVariant(
  status: string | undefined | null,
): BadgeColorVariant {
  switch (status) {
    case "succeeded":
      return "success";
    case "running":
    case "dispatched":
      return "info";
    case "failed":
    case "timeout":
      return "danger";
    case "denied":
      return "governance";
    case "pending":
    case "approval_required":
    case "output_quarantined":
      return "warning";
    default:
      return "default";
  }
}

/**
 * Eval-run score -> badge color. Thresholds: >=95 success, >=80 warning,
 * < 80 danger. Null/undefined renders as default.
 */
export function evalScoreVariant(
  score: number | null | undefined,
): BadgeColorVariant {
  if (score === null || score === undefined) return "default";
  if (score >= 95) return "success";
  if (score >= 80) return "warning";
  return "danger";
}

/**
 * Policy / safety decision badge color. Case-insensitive: handles both the
 * UPPERCASE form used by safety/audit event payloads (`ALLOW`, `DENY`) and
 * the lowercase form used by canonical decision records (`allow`, `deny`,
 * `safety_allow`, `safety_deny`).
 *
 * Unknown decisions return "default". Call sites that prefer a stronger
 * fallback (e.g. SafetyAlertBlock historically defaulted unknowns to
 * "info") should compose this helper accordingly rather than encoding the
 * fallback here.
 */
export function decisionVariant(
  decision: string | null | undefined,
): BadgeColorVariant {
  if (!decision) return "default";
  switch (decision.toLowerCase()) {
    case "allow":
    case "safety_allow":
      return "success";
    case "deny":
    case "safety_deny":
      return "governance";
    case "require_approval":
    case "safety_require_approval":
      return "warning";
    case "throttle":
    case "safety_throttle":
      return "info";
    case "constrain":
    case "allow_with_constraints":
      return "info";
    case "evaluate":
      return "info";
    case "redact":
      return "warning";
    case "pending":
    case "recorded":
      return "info";
    default:
      return "default";
  }
}
