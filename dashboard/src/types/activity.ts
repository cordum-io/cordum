export type ActivityType =
  | "message"
  | "thought"
  | "tool_call"
  | "tool_result"
  | "safety_event"
  | "state_change"
  | "context_update";

export type ActivityRole = "user" | "agent" | "system" | "governance";

export type SafetyDecision = "ALLOW" | "DENY" | "REQUIRE_APPROVAL" | "CONSTRAIN" | "PENDING";

export interface ActivityItem {
  id: string;
  type: ActivityType;
  role: ActivityRole;
  timestamp: string;
  content: string;
  payload?: {
    tool_name?: string;
    tool_inputs?: Record<string, unknown>;
    tool_status?: "pending" | "running" | "success" | "error";
    tool_output?: unknown;
    latency_ms?: number;
    status_code?: number;
    policy_name?: string;
    policy_id?: string;
    decision?: SafetyDecision;
    matched_rules?: string[];
    requires_action?: boolean;
    from_step?: string;
    to_step?: string;
    memory_operation?: "read" | "write" | "delete";
    memory_key?: string;
  };
  metadata?: {
    step_id?: string;
    job_id?: string;
    policy_snapshot?: string;
    cost?: number;
    tokens?: { input: number; output: number };
  };
}
