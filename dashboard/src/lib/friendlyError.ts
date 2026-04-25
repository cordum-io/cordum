import { ApiError } from "@/api/client";
import type { ApprovalConflictPayload } from "@/api/types";
import { logger } from "./logger";

export interface FriendlyError {
  title: string;
  description: string;
  action?: { label: string; href: string };
}

/** Known structured error codes from the backend. */
const ERROR_CODE_MAP: Record<string, FriendlyError> = {
  POLICY_RULE_CONFLICT: {
    title: "Policy rule conflict",
    description: "This rule conflicts with an existing rule. Check rule priorities and matching criteria.",
  },
  BUNDLE_LOCKED: {
    title: "Bundle is locked",
    description: "This bundle is currently locked for editing. Wait for the lock to release or contact the lock owner.",
  },
  WORKFLOW_RUNNING: {
    title: "Workflow is running",
    description: "This workflow has active runs. Stop all runs before making changes.",
  },
  IDEMPOTENCY_CONFLICT: {
    title: "Duplicate request",
    description: "This operation was already submitted. Check the results page for the existing entry.",
  },
  TENANT_MISMATCH: {
    title: "Tenant access denied",
    description: "You don't have access to this tenant's resources. Switch to the correct tenant.",
  },
  approval_already_resolved: {
    title: "Already resolved",
    description: "This approval was already processed. Refresh to review the recorded decision.",
  },
  approval_retryable_lock: {
    title: "Approval is updating",
    description: "Another decision or repair is already in progress. Wait a moment, refresh, and try again.",
  },
  approval_terminal_run: {
    title: "Workflow already moved on",
    description: "This workflow run is no longer waiting on this approval. Refresh to review the final lifecycle state.",
  },
  approval_stale_snapshot: {
    title: "Policy snapshot changed",
    description: "The governing policy changed since this approval was created. Refresh and review the latest request before deciding.",
  },
  approval_stale_request: {
    title: "Approval request is stale",
    description: "The underlying job request changed since this approval was created. Refresh before making a decision.",
  },
  approval_not_actionable: {
    title: "Approval can’t be decided",
    description: "This approval is no longer actionable. Refresh to see its latest lifecycle state.",
  },
};

/** HTTP status code to friendly message mapping. */
const STATUS_MAP: Record<number, FriendlyError> = {
  // 0 is the synthetic status set by `request()` in api/client.ts when the
  // fetch transport itself fails (no internet, DNS fail, CORS reject) —
  // the server never received the request, so retrying is safe.
  0: {
    title: "Unable to connect",
    description: "Check your network connection and try again.",
  },
  400: {
    title: "Invalid request",
    description: "Check your input for missing or invalid fields and try again.",
  },
  408: {
    title: "Request timed out",
    description: "The server took too long to respond. Try again or check system health.",
    action: { label: "Check system health", href: "/settings/health" },
  },
  401: {
    title: "Session expired",
    description: "Your session has expired. Please log in again.",
    action: { label: "Log in", href: "/login" },
  },
  403: {
    title: "Permission denied",
    description: "You don't have permission for this action. Contact your admin if you need access.",
  },
  404: {
    title: "Not found",
    description: "The requested resource no longer exists or was moved.",
  },
  409: {
    title: "Conflict",
    description: "This operation conflicts with the current state. Refresh and try again.",
  },
  422: {
    title: "Validation failed",
    description: "The server rejected the input. Check the form fields and try again.",
  },
  429: {
    title: "Too many requests",
    description: "You're sending requests too quickly. Wait a moment and try again.",
  },
  500: {
    title: "Server error",
    description: "Something went wrong on our end. Try again in a moment.",
    action: { label: "Check system health", href: "/settings/health" },
  },
  502: {
    title: "Service unavailable",
    description: "A backend service is not responding. Check the system health page.",
    action: { label: "Check system health", href: "/settings/health" },
  },
  503: {
    title: "Service temporarily unavailable",
    description: "The system is under maintenance or overloaded. Try again shortly.",
    action: { label: "Check system health", href: "/settings/health" },
  },
  504: {
    title: "Request timed out",
    description: "The server took too long to respond. Try again or check system health.",
    action: { label: "Check system health", href: "/settings/health" },
  },
};

function getStructuredErrorCode(body: unknown): string | undefined {
  if (!body || typeof body !== "object") return undefined;
  const structured = body as ApprovalConflictPayload & { error?: unknown };
  if (typeof structured.code === "string") return structured.code;
  if (typeof structured.error === "string" && ERROR_CODE_MAP[structured.error]) {
    return structured.error;
  }
  return undefined;
}

/**
 * Translates raw errors into user-friendly messages with recovery actions.
 * Always logs the technical error for debugging.
 */
export function friendlyError(err: unknown, context?: string): FriendlyError {
  const logCtx = context ?? "unknown";

  // ApiError with structured body
  if (err instanceof ApiError) {
    logger.error("api-error", `[${logCtx}] ${err.status}: ${err.message}`, {
      status: err.status,
      body: err.body,
    });

    // Check for structured error code in body
    const body = err.body as Record<string, unknown> | null | undefined;
    const structuredCode = getStructuredErrorCode(body);
    if (structuredCode && ERROR_CODE_MAP[structuredCode]) {
      return ERROR_CODE_MAP[structuredCode];
    }

    // Check for validation errors array
    if (body?.errors && Array.isArray(body.errors) && body.errors.length > 0) {
      const fields = body.errors
        .slice(0, 3)
        .map((e: unknown) => {
          if (typeof e === "string") return e;
          if (e && typeof e === "object") {
            const obj = e as Record<string, unknown>;
            const field = obj.field ?? obj.path ?? "";
            const msg = obj.message ?? obj.msg ?? "";
            return field ? `${field}: ${msg}` : String(msg);
          }
          return String(e);
        })
        .filter(Boolean);
      const more = body.errors.length > 3 ? ` (+${body.errors.length - 3} more)` : "";
      return {
        title: "Validation failed",
        description: fields.join(". ") + more,
      };
    }

    // Fall back to status code
    if (STATUS_MAP[err.status]) {
      return STATUS_MAP[err.status];
    }

    return {
      title: `Request failed (${err.status})`,
      description: "An unexpected error occurred. Please try again.",
    };
  }

  // Network errors (fetch failures)
  if (err instanceof TypeError && typeof err.message === "string") {
    const msg = err.message.toLowerCase();
    if (msg.includes("failed to fetch") || msg.includes("network")) {
      logger.error("network-error", `[${logCtx}] ${err.message}`);
      return {
        title: "Unable to connect",
        description: "Check your network connection and try again.",
      };
    }
  }

  // Timeout errors
  if (err instanceof Error && /timeout|deadline|timed.?out/i.test(err.message)) {
    logger.error("timeout-error", `[${logCtx}] ${err.message}`);
    return {
      title: "Request timed out",
      description: "The server is taking too long. Try again or check system health.",
      action: { label: "Check system health", href: "/settings/health" },
    };
  }

  // Generic Error
  if (err instanceof Error) {
    logger.error("error", `[${logCtx}] ${err.message}`);
  } else {
    logger.error("error", `[${logCtx}] ${String(err)}`);
  }

  return {
    title: "Something went wrong",
    description: "An unexpected error occurred. Please try again.",
  };
}
