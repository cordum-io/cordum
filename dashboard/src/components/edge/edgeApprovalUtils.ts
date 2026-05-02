import { ApiError } from "@/api/client";
import type { AgentActionEvent, EdgeApproval, JsonObject, JsonValue } from "@/api/types";
import type { BadgeVariant } from "@/components/ui/StatusBadge";

const SECRET_KEY_PATTERN = /raw|prompt|tool_input|transcript|command_output|authorization|token|secret|password|signed_url/i;

export function approvalStatusVariant(status: string): BadgeVariant {
  switch (status) {
    case "pending":
      return "warning";
    case "approved":
      return "healthy";
    case "rejected":
      return "governance";
    case "expired":
    case "invalidated":
      return "danger";
    default:
      return "muted";
  }
}

export function formatLabel(value: string): string {
  return value.replace(/[_-]/g, " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

export function compactHash(value: string): string {
  if (value.length <= 18) return value;
  return `${value.slice(0, 10)}…${value.slice(-6)}`;
}

export function isExpired(approval: EdgeApproval, now = Date.now()): boolean {
  if (!approval.expiresAt) return false;
  const expiresAt = Date.parse(approval.expiresAt);
  return Number.isFinite(expiresAt) && expiresAt <= now;
}

export function isTerminal(approval: EdgeApproval): boolean {
  return approval.status !== "pending";
}

export function isSelfApproval(approval: EdgeApproval, principalId?: string): boolean {
  if (!principalId?.trim()) return false;
  return approval.requester === principalId || approval.principalId === principalId;
}

export function matchingEvent(
  approval: EdgeApproval,
  events: AgentActionEvent[],
): AgentActionEvent | undefined {
  return events.find((event) => event.eventId === approval.eventId || event.inputHash === approval.inputHash);
}

export function actionSummary(approval: EdgeApproval, event?: AgentActionEvent): string {
  return (
    event?.actionName ||
    event?.toolName ||
    event?.capability ||
    event?.kind ||
    approval.metadata?.action ||
    "Governed Edge action"
  );
}

function safeJsonValue(value: JsonValue | undefined): JsonValue | undefined {
  if (Array.isArray(value)) {
    return value.map(safeJsonValue).filter((item): item is JsonValue => item !== undefined);
  }
  if (value && typeof value === "object") {
    const out: JsonObject = {};
    for (const [key, nested] of Object.entries(value)) {
      if (!SECRET_KEY_PATTERN.test(key)) {
        const safe = safeJsonValue(nested);
        if (safe !== undefined) out[key] = safe;
      }
    }
    return out;
  }
  return value;
}

export function redactedInputPreview(event?: AgentActionEvent): string {
  const safe = safeJsonValue(event?.inputRedacted ?? undefined);
  if (!safe || (typeof safe === "object" && !Array.isArray(safe) && Object.keys(safe).length === 0)) {
    return "Redacted input is available from the linked event when the API returns it.";
  }
  return JSON.stringify(safe, null, 2);
}

export function isNotVisibleError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404;
}
