import { useState, useCallback, useMemo } from "react";
import { useSearchParams, useNavigate } from "react-router-dom";
import { FlaskConical } from "lucide-react";
import { motion } from "framer-motion";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { SimulatorContextForm, type SimulatorContext } from "@/components/policy/simulator/SimulatorContextForm";
import { SimulatorResultsChain } from "@/components/policy/simulator/SimulatorResultsChain";
import { SimulatorDecisionSummary } from "@/components/policy/simulator/SimulatorDecisionSummary";
import { usePolicyStudioGlobal } from "@/hooks/usePolicyStudioGlobal";
import { useExplainPolicy, type ExplainResult } from "@/hooks/usePolicies";

export const SIMULATOR_PAGE_SECTIONS = [
  "bundle-select",
  "context-form",
  "decision-summary",
  "evaluation-chain",
] as const;

/** Build the request payload from structured form context. */
export function buildSimulatorRequest(
  ctx: SimulatorContext,
): Record<string, unknown> {
  const req: Record<string, unknown> = {};
  if (ctx.topic.trim()) req.topic = ctx.topic.trim();
  if (ctx.tenant.trim()) req.tenant_id = ctx.tenant.trim();
  if (ctx.workflowId.trim()) req.workflow_id = ctx.workflowId.trim();
  if (ctx.capabilities.length > 0) req.capabilities = ctx.capabilities;
  if (ctx.riskTags.length > 0) req.risk_tags = ctx.riskTags;
  if (Object.keys(ctx.labels).length > 0) req.labels = ctx.labels;
  return req;
}

/** Parse deep-link query params into a prefilled SimulatorContext. */
export function parseSimulatorQueryParams(
  params: URLSearchParams,
): Partial<SimulatorContext> {
  const result: Partial<SimulatorContext> = {};
  const topic = params.get("topic");
  if (topic) result.topic = topic;
  const tenant = params.get("tenant");
  if (tenant) result.tenant = tenant;
  const workflowId = params.get("workflow");
  if (workflowId) result.workflowId = workflowId;
  const caps = params.get("capabilities");
  if (caps) result.capabilities = caps.split(",").map((s) => s.trim()).filter(Boolean);
  const risks = params.get("risk_tags");
  if (risks) result.riskTags = risks.split(",").map((s) => s.trim()).filter(Boolean);
  return result;
}

export default function SimulatorPage({ hideHeader }: { hideHeader?: boolean } = {}) {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const prefill = useMemo(() => parseSimulatorQueryParams(searchParams), [searchParams]);
  const initialBundleId = searchParams.get("bundle") ?? "";

  const {
    bundles,
    selectedBundleId,
    setSelectedBundleId,
    policy,
    simulate,
    simulateError,
    clearSimulateError,
    isLoading,
    isSimulating,
    loadError,
    refetchBundles,
    refetchSelectedBundle,
  } = usePolicyStudioGlobal(initialBundleId);

  const explainMutation = useExplainPolicy();
  const [explainResult, setExplainResult] = useState<ExplainResult | null>(null);
  const [explainError, setExplainError] = useState<string | null>(null);

  const ruleCount = policy.rules.length;

  const handleSimulate = useCallback(
    async (ctx: SimulatorContext) => {
      clearSimulateError();
      setExplainResult(null);
      setExplainError(null);

      const request = buildSimulatorRequest(ctx);

      // Run simulate + explain in parallel
      const [simResult, explainRes] = await Promise.allSettled([
        simulate(request),
        explainMutation.mutateAsync({ request }),
      ]);

      if (explainRes.status === "fulfilled") {
        setExplainResult(explainRes.value);
      } else {
        setExplainError(
          explainRes.reason instanceof Error
            ? explainRes.reason.message
            : "Explain request failed",
        );
      }

      if (simResult.status === "fulfilled" && !simResult.value.ok) {
        // simulate error already set via usePolicyStudioGlobal
      }
    },
    [simulate, explainMutation, clearSimulateError],
  );

  const simResult = useMemo(() => {
    if (!explainResult) return null;
    return {
      decision: explainResult.decision,
      matchedRule: explainResult.matchedRule,
      reason: explainResult.reason,
      evaluationTimeMs: explainResult.evaluationTimeMs,
    };
  }, [explainResult]);

  if (isLoading && bundles.length === 0) {
    return (
      <div className="space-y-6">
        {!hideHeader && <PageHeader label="Govern" title="Simulator" subtitle="Loading simulation context..." />}
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <SkeletonCard />
          <SkeletonCard />
          <SkeletonCard />
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {!hideHeader && <PageHeader
        label="Govern"
        title="Simulator"
        subtitle="Dry-run policy evaluation against bundles and rules. Select a bundle, configure a test payload, and inspect the evaluation chain."
        actions={
          <StatusBadge variant="info">all roles</StatusBadge>
        }
      />}

      {loadError && (
        <InfoBanner variant="error" title="Unable to load simulation context">
          <p>{loadError.message}</p>
          {loadError.details && <p className="mt-1 text-destructive">{loadError.details}</p>}
          <Button
            variant="outline"
            size="sm"
            className="mt-2"
            onClick={() => {
              void refetchBundles();
              if (selectedBundleId) void refetchSelectedBundle();
            }}
          >
            Retry
          </Button>
        </InfoBanner>
      )}

      {!isLoading && bundles.length === 0 ? (
        <EmptyState
          icon={<FlaskConical className="w-6 h-6" />}
          title="No bundles available for simulation"
          description="At least one policy bundle is required before running a simulation."
          action={
            <Button variant="outline" size="sm" onClick={() => navigate("/govern/overview?tab=bundles")}>
              Review bundles
            </Button>
          }
        />
      ) : (
        <motion.div
          className="space-y-6"
          initial="hidden"
          animate="visible"
          variants={{ visible: { transition: { staggerChildren: 0.05 } } }}
        >
          {/* Bundle selector + summary */}
          <motion.div
            className="flex flex-wrap items-center justify-between gap-3"
            variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}
          >
            <label className="text-xs text-muted-foreground">
              Bundle
              <select
                id="simulator-bundle-select"
                className="ml-2 h-8 rounded-2xl border border-border bg-surface-2 px-2 text-xs text-foreground"
                value={selectedBundleId}
                onChange={(e) => setSelectedBundleId(e.target.value)}
              >
                {bundles.map((b) => (
                  <option key={b.id} value={b.id}>
                    {b.name || b.id}
                  </option>
                ))}
              </select>
            </label>
            <div className="flex items-center gap-3 text-xs text-muted-foreground">
              <span className="font-mono">{bundles.length} bundles</span>
              <span className="font-mono">{ruleCount} rules</span>
              <StatusBadge variant={ruleCount > 0 ? "healthy" : "warning"}>
                {ruleCount > 0 ? "ready" : "no rules"}
              </StatusBadge>
            </div>
          </motion.div>

          {/* Simulation workspace */}
          <motion.div
            className="grid grid-cols-1 lg:grid-cols-2 gap-6"
            variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}
          >
            {/* Left: context form */}
            <div className="space-y-4">
              <SimulatorContextForm
                prefill={prefill}
                isSubmitting={isSimulating || explainMutation.isPending}
                onSubmit={handleSimulate}
              />
            </div>

            {/* Right: results */}
            <div className="space-y-4">
              {simulateError && (
                <InfoBanner variant="error" title="Simulation failed">
                  {simulateError.message}
                  {simulateError.details && (
                    <p className="mt-1 text-destructive">{simulateError.details}</p>
                  )}
                </InfoBanner>
              )}

              {explainError && (
                <InfoBanner variant="warning" title="Explain unavailable">
                  {explainError}
                </InfoBanner>
              )}

              {simResult && (
                <SimulatorDecisionSummary
                  decision={simResult.decision}
                  matchedRule={simResult.matchedRule}
                  reason={simResult.reason}
                  evaluationTimeMs={simResult.evaluationTimeMs}
                />
              )}

              {explainResult && (
                <SimulatorResultsChain chain={explainResult.evaluationChain} />
              )}

              {!simResult && !simulateError && !explainError && (
                <div className="instrument-card p-6 text-center">
                  <FlaskConical className="w-8 h-8 mx-auto mb-3 text-muted-foreground/40" />
                  <p className="text-sm text-muted-foreground">
                    Configure a test payload and run the simulator to see evaluation results.
                  </p>
                </div>
              )}
            </div>
          </motion.div>
        </motion.div>
      )}
    </div>
  );
}
