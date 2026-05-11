import { useEffect, useRef, useState } from "react";
import { evaluatePolicy } from "@/api/generated/policy/policy";
import type { EdgeEvaluationContext } from "@/api/generated/model/edgeEvaluationContext";
import type { EvaluatePolicy200 } from "@/api/generated/model/evaluatePolicy200";
import type { JobEvaluationContext } from "@/api/generated/model/jobEvaluationContext";
import type { PolicyAuditEntry } from "@/api/generated/model/policyAuditEntry";
import { RuleType } from "@/api/generated/model/ruleType";
import type { NormalizedRule } from "@/hooks/useRulesList";
import { logger } from "@/lib/logger";

export type SimulatorBucket =
  | "allow"
  | "deny"
  | "require_human"
  | "throttle"
  | "quarantine"
  | "redact"
  | "allow_with_constraints"
  | "error";

export interface SimulatorOutcome {
  entryId: string;
  bucket: SimulatorBucket;
  /** Resource id from the audit entry (rule id when applicable). */
  resourceId: string;
  /** When bucket === "error", this carries the human-readable reason. */
  errorMessage?: string;
}

export interface SimulatorRunResult {
  outcomes: SimulatorOutcome[];
  /** Counts per bucket; always populated for every known bucket key. */
  counts: Record<SimulatorBucket, number>;
  /** Wall-clock ms for the full Promise.allSettled batch. */
  elapsedMs: number;
  /** Whether the run was prevented from starting (e.g. unsupported type). */
  disabled: boolean;
  /** Reason set when disabled === true. */
  disabledReason?: string;
}

interface UseEvaluateRuleArgs {
  /**
   * Draft rule under inspection. NormalizedRule's `type` is widened to
   * `RuleType | "unknown"` for the dashboard's UNKNOWN_RULE_TYPE
   * sentinel; the hook's runtime check filters out the unknown case
   * before calling evaluatePolicy.
   */
  rule: NormalizedRule | null;
  entries: ReadonlyArray<PolicyAuditEntry> | null | undefined;
  /** Set false to prevent kicking the batch (e.g. while the user types). */
  enabled?: boolean;
}

/**
 * Per-entry context reconstruction from a PolicyAuditEntry. Audit entries
 * don't carry full evaluation contexts, so we synthesize the minimum
 * required envelope: tenant_id (from extra), topic (from extra/action),
 * principal_id (actor/agent fallback). When the synthesis can't satisfy
 * the required fields the entry lands in the error bucket.
 */
function entryToJobContext(entry: PolicyAuditEntry): JobEvaluationContext | null {
  const extra = entry.extra ?? {};
  const tenant_id = readString(extra, "tenant_id") ?? "default";
  const topic = readString(extra, "topic") ?? entry.action;
  if (!topic) return null;
  const ctx: JobEvaluationContext = { tenant_id, topic };
  const job_id = readString(extra, "job_id");
  if (job_id) ctx.job_id = job_id;
  const workflow_id = readString(extra, "workflow_id");
  if (workflow_id) ctx.workflow_id = workflow_id;
  const principal_id = entry.actor_id ?? entry.agent_id ?? null;
  if (principal_id) ctx.principal_id = principal_id;
  const memory_id = readString(extra, "memory_id");
  if (memory_id) ctx.memory_id = memory_id;
  const capability = readString(extra, "capability");
  if (capability) ctx.capability = capability;
  return ctx;
}

function entryToEdgeContext(entry: PolicyAuditEntry): EdgeEvaluationContext | null {
  const extra = entry.extra ?? {};
  const tenant_id = readString(extra, "tenant_id") ?? "default";
  const principal_id = entry.actor_id ?? entry.agent_id ?? readString(extra, "principal_id");
  if (!principal_id) return null;
  const ctx: EdgeEvaluationContext = { tenant_id, principal_id };
  const session_id = readString(extra, "session_id");
  if (session_id) ctx.session_id = session_id;
  const execution_id = readString(extra, "execution_id");
  if (execution_id) ctx.execution_id = execution_id;
  const tool_name = readString(extra, "tool_name");
  if (tool_name) ctx.tool_name = tool_name;
  const input_hash = readString(extra, "input_hash");
  if (input_hash) ctx.input_hash = input_hash;
  return ctx;
}

function readString(o: Record<string, string>, key: string): string | undefined {
  const v = o[key];
  return typeof v === "string" && v.length > 0 ? v : undefined;
}

const KNOWN_BUCKETS: ReadonlyArray<SimulatorBucket> = [
  "allow",
  "deny",
  "require_human",
  "throttle",
  "quarantine",
  "redact",
  "allow_with_constraints",
  "error",
];

function emptyCounts(): Record<SimulatorBucket, number> {
  const out = {} as Record<SimulatorBucket, number>;
  for (const bucket of KNOWN_BUCKETS) out[bucket] = 0;
  return out;
}

/**
 * Maps a backend Decision response into a known simulator bucket. Two
 * response shapes are supported during the migration window:
 * - PolicyEvaluateResponse (unified): `{decision: Decision { type, ... }}`
 *   — `decision` is an object; the bucket key lives on `decision.type`.
 * - PolicyCheckResponse (legacy): `{decision?: <DecisionType string>, ...}`
 *   — `decision` is a flat string.
 * Unknown decisions / shapes fall to "error" so totals add to entry count.
 */
function bucketFor(response: EvaluatePolicy200): SimulatorBucket {
  const obj = response as Record<string, unknown>;
  // Unified path: `decision` is an object with a `type` discriminator.
  const decisionRaw = obj.decision;
  if (
    decisionRaw &&
    typeof decisionRaw === "object" &&
    !Array.isArray(decisionRaw)
  ) {
    const nested = (decisionRaw as Record<string, unknown>).type;
    if (typeof nested === "string") {
      const norm = nested.toLowerCase();
      if ((KNOWN_BUCKETS as ReadonlyArray<string>).includes(norm)) {
        return norm as SimulatorBucket;
      }
    }
  }
  // Legacy path: `decision` is a flat string.
  if (typeof decisionRaw === "string") {
    const norm = decisionRaw.toLowerCase();
    if ((KNOWN_BUCKETS as ReadonlyArray<string>).includes(norm)) {
      return norm as SimulatorBucket;
    }
  }
  // Other historical shapes nest `decision_type` or `outcome` directly.
  const fallback = (obj.decision_type ?? obj.outcome) as unknown;
  if (typeof fallback === "string") {
    const norm = fallback.toLowerCase();
    if ((KNOWN_BUCKETS as ReadonlyArray<string>).includes(norm)) {
      return norm as SimulatorBucket;
    }
  }
  return "error";
}

/**
 * Runs the draft `rule` against each `entry` via Promise.allSettled and
 * buckets the results. Failures (network, shape, single-event reject)
 * land in the error bucket and don't crash the batch. The hook is
 * *imperative*: results live in component state and re-run when the
 * deps change.
 */
export function useEvaluateRule({
  rule,
  entries,
  enabled = true,
}: UseEvaluateRuleArgs): SimulatorRunResult | null {
  const [result, setResult] = useState<SimulatorRunResult | null>(null);
  // The latest call wins — if `rule` or `entries` change mid-flight we
  // discard the older batch's results.
  const runIdRef = useRef(0);

  useEffect(() => {
    if (!enabled || !rule || !entries) {
      setResult(null);
      return;
    }
    const ruleType = rule.type;
    const isJob =
      ruleType === RuleType.input ||
      ruleType === RuleType.output ||
      ruleType === RuleType.velocity;
    const isEdge = ruleType === RuleType.edge;
    if (!isJob && !isEdge) {
      setResult({
        outcomes: [],
        counts: emptyCounts(),
        elapsedMs: 0,
        disabled: true,
        disabledReason: `Rule type "${String(ruleType)}" is not yet supported by the simulator.`,
      });
      return;
    }

    const myRun = ++runIdRef.current;
    const start = performance.now();
    const abort = new AbortController();

    const tasks = entries.map(async (entry) => {
      const context = isJob
        ? entryToJobContext(entry)
        : entryToEdgeContext(entry);
      if (!context) {
        const reason = isJob
          ? "audit entry missing topic/extras for job context"
          : "audit entry missing principal_id for edge context";
        return { entry, error: reason };
      }
      try {
        // The runtime check above narrows `rule.type` to a known RuleType
        // (UNKNOWN_RULE_TYPE returns the disabled state). Cast to the
        // strict `Rule` shape that the generated client expects.
        const ruleForApi = rule as unknown as import("@/api/generated/model/rule").Rule;
        const response = await evaluatePolicy(
          isJob
            ? { rule: ruleForApi, job_context: context as JobEvaluationContext }
            : { rule: ruleForApi, edge_context: context as EdgeEvaluationContext },
          abort.signal,
        );
        return { entry, response };
      } catch (err) {
        return {
          entry,
          error: err instanceof Error ? err.message : "evaluator request failed",
        };
      }
    });

    const cleanup = () => {
      abort.abort();
    };

    void Promise.allSettled(tasks).then((settled) => {
      if (runIdRef.current !== myRun) return; // a newer run took over
      const counts = emptyCounts();
      const outcomes: SimulatorOutcome[] = [];
      for (const s of settled) {
        if (s.status !== "fulfilled") {
          // allSettled on async fns shouldn't reject (errors are caught
          // inside the task) but defense-in-depth: count it.
          counts.error += 1;
          outcomes.push({
            entryId: "",
            bucket: "error",
            resourceId: "",
            errorMessage: "task rejected unexpectedly",
          });
          continue;
        }
        const v = s.value;
        if ("error" in v) {
          counts.error += 1;
          outcomes.push({
            entryId: v.entry.id,
            bucket: "error",
            resourceId: v.entry.resource_id ?? "",
            errorMessage: v.error,
          });
          continue;
        }
        const bucket = bucketFor(v.response);
        counts[bucket] += 1;
        outcomes.push({
          entryId: v.entry.id,
          bucket,
          resourceId: v.entry.resource_id ?? "",
        });
      }
      const elapsedMs = performance.now() - start;
      logger.info("policy-studio-simulator", "batch complete", {
        n: entries.length,
        elapsedMs,
        errors: counts.error,
      });
      setResult({
        outcomes,
        counts,
        elapsedMs,
        disabled: false,
      });
    });
    return cleanup;
  }, [enabled, rule, entries]);

  return result;
}

export const SIMULATOR_BUCKETS = KNOWN_BUCKETS;
