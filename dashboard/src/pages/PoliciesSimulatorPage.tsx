/*
 * DESIGN: "Control Surface" — Policy Simulator v2
 * Spec: Scope-aware simulation, explain chain, output scanner tester
 */
import { useState, useMemo } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { PolicyStudioLayout } from "@/components/layout/PolicyStudioLayout";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";
import {
  Play, RotateCcw, Code, CheckCircle2, XCircle,
  AlertTriangle, Shield, Zap,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { toast } from "sonner";
import { usePolicyBundles, useSimulatePolicy, useExplainPolicy, type SimulateResult, type ExplainResult } from "@/hooks/usePolicies";
import { useWorkflows } from "@/hooks/useWorkflows";

const SCOPES = ["global", "workflow", "tenant"] as const;
type Scope = (typeof SCOPES)[number];

const SAMPLE_PAYLOADS = [
  { label: "Safe Job", value: JSON.stringify({ topic: "data.export", pool: "default", capabilities: ["read"], risk_tags: [] }, null, 2) },
  { label: "Risky Job", value: JSON.stringify({ topic: "admin.delete", pool: "production", capabilities: ["write", "admin"], risk_tags: ["destructive", "pii"] }, null, 2) },
  { label: "MCP Call", value: JSON.stringify({ topic: "mcp.tool_call", mcp_server: "github-mcp", mcp_tool: "create_issue", pool: "default", risk_tags: ["external"] }, null, 2) },
];

interface SimResult {
  decision: string;
  latency: string;
  rules: { name: string; result: string; reason?: string }[];
}

interface ExplainStep {
  rule: string;
  result: string;
  reason?: string;
  priority?: number;
}

function decisionIcon(d: string) {
  switch (d) {
    case "allow": return <CheckCircle2 className="w-6 h-6 text-emerald-400" />;
    case "deny": return <XCircle className="w-6 h-6 text-red-400" />;
    case "require_approval": return <AlertTriangle className="w-6 h-6 text-amber-400" />;
    case "allow_with_constraints": return <Shield className="w-6 h-6 text-blue-400" />;
    default: return <Zap className="w-6 h-6 text-muted-foreground" />;
  }
}

function mapDecision(raw: string): string {
  const lower = (raw || "").toLowerCase();
  if (lower.includes("deny") || lower.includes("block")) return "deny";
  if (lower.includes("warn") || lower.includes("approval")) return "require_approval";
  if (lower.includes("constraint")) return "allow_with_constraints";
  return "allow";
}

export default function PoliciesSimulatorPage() {
  const { data: bundlesData } = usePolicyBundles();
  const { data: workflowsData } = useWorkflows();
  const bundles = bundlesData?.items ?? [];
  const workflows = workflowsData ?? [];
  const simulateMutation = useSimulatePolicy();
  const explainMutation = useExplainPolicy();

  const [scope, setScope] = useState<Scope>("global");
  const [workflowId, setWorkflowId] = useState("");
  const [tenantId, setTenantId] = useState("");
  const [selectedBundleId, setSelectedBundleId] = useState("");
  const [payload, setPayload] = useState(SAMPLE_PAYLOADS[0].value);
  const [mode, setMode] = useState<"simulate" | "explain">("simulate");
  const [result, setResult] = useState<SimResult | null>(null);
  const [explainSteps, setExplainSteps] = useState<ExplainStep[] | null>(null);
  const [explainDecision, setExplainDecision] = useState<string | null>(null);

  useMemo(() => {
    if (!selectedBundleId && bundles.length > 0) setSelectedBundleId(bundles[0].id);
  }, [bundles, selectedBundleId]);

  const parsePayload = (): Record<string, unknown> | null => {
    try {
      const parsed = JSON.parse(payload);
      if (scope === "workflow" && workflowId) parsed.workflow_id = workflowId;
      if (scope === "tenant" && tenantId) parsed.tenant = tenantId;
      return parsed;
    } catch {
      toast.error("Invalid JSON payload");
      return null;
    }
  };

  const handleSimulate = () => {
    if (!selectedBundleId) { toast.error("Select a bundle first"); return; }
    const parsed = parsePayload();
    if (!parsed) return;
    simulateMutation.mutate(
      { bundleId: selectedBundleId, request: parsed },
      {
        onSuccess: (data: SimulateResult) => {
          setResult({
            decision: mapDecision(data.decision ?? "allow"),
            latency: data.evaluationTimeMs ? `${data.evaluationTimeMs}ms` : "—",
            rules: [{ name: data.matchedRule || "policy", result: mapDecision(data.decision ?? "allow") === "deny" ? "fail" : "pass", reason: data.reason }],
          });
          setExplainSteps(null);
          setExplainDecision(null);
        },
      },
    );
  };

  const handleExplain = () => {
    if (!selectedBundleId) { toast.error("Select a bundle first"); return; }
    const parsed = parsePayload();
    if (!parsed) return;
    explainMutation.mutate(
      { request: { bundle_id: selectedBundleId, ...parsed } },
      {
        onSuccess: (data: ExplainResult) => {
          setExplainSteps(
            (data.evaluationChain ?? []).map((s) => ({
              rule: s.ruleName ?? s.ruleId ?? "unknown",
              result: s.decision || (s.matched ? "pass" : "fail"),
              reason: s.reason,
            })),
          );
          setExplainDecision(data.decision ?? "allow");
          setResult(null);
        },
        onError: (err: Error) => toast.error("Explain failed", { description: err.message }),
      },
    );
  };

  const handleRun = () => (mode === "simulate" ? handleSimulate() : handleExplain());

  return (
    <PolicyStudioLayout>
      <div className="space-y-6">
        {/* Toolbar */}
        <div className="flex items-center justify-between flex-wrap gap-3">
          <div className="flex items-center gap-3 flex-wrap">
            <div className="flex items-center gap-1 bg-surface-1 rounded-lg p-0.5 border border-border">
              {SCOPES.map((s) => (
                <button key={s} onClick={() => setScope(s)}
                  className={cn("px-3 py-1.5 text-xs font-mono rounded-md transition-all", scope === s ? "bg-cordum/15 text-cordum font-medium" : "text-muted-foreground hover:text-foreground")}>
                  {s.charAt(0).toUpperCase() + s.slice(1)}
                </button>
              ))}
            </div>
            {scope === "workflow" && (
              <select value={workflowId} onChange={(e) => setWorkflowId(e.target.value)}
                className="h-8 px-2 text-xs font-mono bg-surface-1 border border-border rounded-md text-foreground focus:outline-none focus:ring-1 focus:ring-cordum">
                <option value="">Select workflow…</option>
                {workflows.map((w: { id: string; name: string }) => <option key={w.id} value={w.id}>{w.name}</option>)}
              </select>
            )}
            {scope === "tenant" && (
              <input type="text" value={tenantId} onChange={(e) => setTenantId(e.target.value)} placeholder="tenant-id"
                className="h-8 w-48 px-3 text-xs font-mono bg-surface-1 border border-border rounded-md text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum" />
            )}
            <select value={selectedBundleId} onChange={(e) => setSelectedBundleId(e.target.value)}
              className="h-8 px-2 text-xs font-mono bg-surface-1 border border-border rounded-md text-foreground focus:outline-none focus:ring-1 focus:ring-cordum">
              {bundles.length === 0 && <option value="">No bundles</option>}
              {bundles.map((b) => <option key={b.id} value={b.id}>{b.name} ({b.id})</option>)}
            </select>
          </div>
          <div className="flex items-center gap-1 bg-surface-1 rounded-lg p-0.5 border border-border">
            <button onClick={() => setMode("simulate")}
              className={cn("px-3 py-1.5 text-xs font-mono rounded-md transition-all", mode === "simulate" ? "bg-cordum/15 text-cordum font-medium" : "text-muted-foreground hover:text-foreground")}>
              Simulate
            </button>
            <button onClick={() => setMode("explain")}
              className={cn("px-3 py-1.5 text-xs font-mono rounded-md transition-all", mode === "explain" ? "bg-cordum/15 text-cordum font-medium" : "text-muted-foreground hover:text-foreground")}>
              Explain
            </button>
          </div>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Input */}
          <div className="space-y-4">
            <div className="flex items-center justify-between">
              <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Test Payload</p>
              <div className="flex gap-1">
                {SAMPLE_PAYLOADS.map((sp) => (
                  <button key={sp.label} onClick={() => { setPayload(sp.value); setResult(null); setExplainSteps(null); }}
                    className="px-2 py-1 text-[10px] font-mono rounded bg-surface-1 text-muted-foreground hover:text-foreground transition-colors">
                    {sp.label}
                  </button>
                ))}
              </div>
            </div>
            <div className="instrument-card p-0 overflow-hidden">
              <textarea value={payload} onChange={(e) => setPayload(e.target.value)}
                className="w-full h-64 p-4 text-xs font-mono bg-transparent text-foreground resize-none focus:outline-none" spellCheck={false} />
            </div>
            <div className="flex gap-2">
              <Button variant="primary" size="sm" onClick={handleRun}
                loading={simulateMutation.isPending || explainMutation.isPending}
                disabled={!selectedBundleId || simulateMutation.isPending || explainMutation.isPending}>
                <Play className="w-3 h-3 mr-1" />{mode === "simulate" ? "Simulate" : "Explain"}
              </Button>
              <Button variant="ghost" size="sm" onClick={() => { setPayload(SAMPLE_PAYLOADS[0].value); setResult(null); setExplainSteps(null); }}>
                <RotateCcw className="w-3 h-3 mr-1" />Reset
              </Button>
            </div>
          </div>

          {/* Result */}
          <div className="space-y-4">
            <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">
              {mode === "simulate" ? "Simulation Result" : "Explain Chain"}
            </p>
            <AnimatePresence mode="wait">
              {result ? (
                <motion.div key="sim" initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }} className="space-y-4">
                  <div className={cn("instrument-card p-5", result.decision === "allow" ? "status-healthy" : result.decision === "deny" ? "status-danger" : "status-warning")}>
                    <div className="flex items-center gap-3">
                      {decisionIcon(result.decision)}
                      <div>
                        <span className="text-lg font-display font-bold text-foreground capitalize">{result.decision.replace(/_/g, " ")}</span>
                        <p className="text-xs text-muted-foreground font-mono">Evaluated in {result.latency}</p>
                      </div>
                    </div>
                  </div>
                  {result.rules.length > 0 && (
                    <div className="instrument-card overflow-hidden">
                      <div className="px-4 py-3 bg-surface-0 border-b border-border">
                        <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Matched Rules</p>
                      </div>
                      <div className="divide-y divide-border">
                        {result.rules.map((rule) => (
                          <div key={rule.name} className="flex items-center justify-between px-4 py-3">
                            <div>
                              <span className="text-xs font-mono text-foreground">{rule.name}</span>
                              {rule.reason && <p className="text-[10px] text-muted-foreground">{rule.reason}</p>}
                            </div>
                            <StatusBadge variant={rule.result === "pass" ? "healthy" : "danger"}>{rule.result}</StatusBadge>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </motion.div>
              ) : explainSteps ? (
                <motion.div key="explain" initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }} className="space-y-4">
                  <div className={cn("instrument-card p-5", explainDecision === "allow" ? "status-healthy" : explainDecision === "deny" ? "status-danger" : "status-warning")}>
                    <div className="flex items-center gap-3">
                      {decisionIcon(explainDecision ?? "allow")}
                      <div>
                        <span className="text-lg font-display font-bold text-foreground capitalize">{(explainDecision ?? "allow").replace(/_/g, " ")}</span>
                        <p className="text-xs text-muted-foreground font-mono">{explainSteps.length} rules evaluated</p>
                      </div>
                    </div>
                  </div>
                  <div className="instrument-card overflow-hidden">
                    <div className="px-4 py-3 bg-surface-0 border-b border-border">
                      <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Evaluation Chain</p>
                    </div>
                    <div className="relative">
                      <div className="absolute left-6 top-0 bottom-0 w-px bg-border" />
                      {explainSteps.map((step, i) => (
                        <div key={i} className="relative flex items-start gap-4 px-4 py-3">
                          <div className="relative z-10 w-5 h-5 rounded-full bg-surface-1 border-2 border-border flex items-center justify-center shrink-0 mt-0.5">
                            <div className={cn("w-2 h-2 rounded-full",
                              step.result === "pass" || step.result === "allow" ? "bg-emerald-400" :
                              step.result === "fail" || step.result === "deny" ? "bg-red-400" : "bg-amber-400")} />
                          </div>
                          <div className="flex-1 min-w-0">
                            <div className="flex items-center gap-2">
                              <span className="text-xs font-mono text-foreground">{step.rule}</span>
                              {step.priority != null && <span className="text-[10px] font-mono text-muted-foreground">P{step.priority}</span>}
                              <StatusBadge variant={step.result === "pass" || step.result === "allow" ? "healthy" : step.result === "fail" || step.result === "deny" ? "danger" : "warning"}>
                                {step.result}
                              </StatusBadge>
                            </div>
                            {step.reason && <p className="text-[10px] text-muted-foreground mt-0.5">{step.reason}</p>}
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>
                </motion.div>
              ) : (
                <motion.div key="empty" initial={{ opacity: 0 }} animate={{ opacity: 1 }} className="instrument-card p-12 text-center">
                  <Code className="w-8 h-8 text-muted-foreground/30 mx-auto mb-3" />
                  <p className="text-sm text-muted-foreground">Click &ldquo;{mode === "simulate" ? "Simulate" : "Explain"}&rdquo; to test your payload</p>
                </motion.div>
              )}
            </AnimatePresence>
          </div>
        </div>
      </div>
    </PolicyStudioLayout>
  );
}
