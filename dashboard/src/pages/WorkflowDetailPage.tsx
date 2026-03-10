/*
 * DESIGN: "Control Surface" — Workflow Detail
 * Visual DAG with step detail panel on click
 */
import { useParams, useNavigate } from "react-router-dom";
import { motion } from "framer-motion";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { Skeleton } from "@/components/ui/Skeleton";
import { ArrowLeft, Play, Edit, GitBranch, Workflow, Eye, Shield, Save, Clock, CheckCircle2, XCircle, Layers } from "lucide-react";
import { RunDAG } from "@/components/workflows/dag/RunDAG";
import { NodeDetailPanel } from "@/components/workflows/dag/NodeDetailPanel";
import { useMemo, useState } from "react";
import { cn, formatRelativeTime, clickableRowProps } from "@/lib/utils";
import { useWorkflow, useRuns, useStartRun, useUpdateWorkflow } from "@/hooks/useWorkflows";
import { useRunStream } from "@/hooks/useRunStream";
import { toast } from "sonner";
import type { PolicyConstraints, WorkflowRun, WorkflowStep } from "@/api/types";
import { WorkflowPolicyOverrides, extractConstraints } from "@/components/workflows/WorkflowPolicyOverrides";
import { WorkflowPolicyOverrideRules, extractWorkflowRules } from "@/components/workflows/WorkflowPolicyOverrideRules";
import { InstrumentCardHeader } from "@/components/ui/InstrumentCard";

export default function WorkflowDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [activeTab, setActiveTab] = useState("diagram");
  const startRun = useStartRun();
  const updateWorkflow = useUpdateWorkflow();
  const [constraintDraft, setConstraintDraft] = useState<PolicyConstraints | null>(null);
  const [selectedStep, setSelectedStep] = useState<WorkflowStep | null>(null);
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);

  useRunStream(null);

  const { data: workflow, isLoading } = useWorkflow(id);
  const { data: runs } = useRuns(id);

  // Find the selected run for DAG overlay
  const selectedRun = useMemo(
    () => (selectedRunId ? (runs ?? []).find((r: WorkflowRun) => r.id === selectedRunId) ?? null : null),
    [runs, selectedRunId],
  );

  // Latest run for quick-select
  const latestRun = useMemo(() => {
    if (!runs?.length) return null;
    return [...runs].sort((a: WorkflowRun, b: WorkflowRun) =>
      new Date(b.startedAt ?? b.createdAt ?? 0).getTime() - new Date(a.startedAt ?? a.createdAt ?? 0).getTime()
    )[0] as WorkflowRun;
  }, [runs]);

  const savedConstraints = useMemo(
    () => extractConstraints(workflow?.config, workflow?.metadata),
    [workflow?.config, workflow?.metadata],
  );
  const workflowRules = useMemo(
    () => extractWorkflowRules(workflow?.config, workflow?.metadata),
    [workflow?.config, workflow?.metadata],
  );
  const activeConstraints = constraintDraft ?? savedConstraints;
  const constraintsDirty = constraintDraft !== null;

  const saveConstraints = async () => {
    if (!workflow || !constraintDraft) return;
    try {
      await updateWorkflow.mutateAsync({
        id: workflow.id,
        name: workflow.name,
        config: { ...(workflow.config ?? {}), constraints: constraintDraft },
      });
      setConstraintDraft(null);
      toast.success("Policy overrides saved");
    } catch {
      toast.error("Failed to save policy overrides");
    }
  };

  const handleNodeClick = (stepId: string) => {
    const step = (workflow?.steps ?? []).find((s) => s.id === stepId) ?? null;
    setSelectedStep(step);
  };

  if (isLoading) {
    return (
      <div className="space-y-6">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }

  if (!workflow) {
    return (
      <EmptyState
        icon={<Workflow className="w-5 h-5" />}
        title="Workflow not found"
        action={
          <Button variant="outline" size="sm" onClick={() => navigate("/workflows")}>
            <ArrowLeft className="w-3 h-3 mr-1" />
            Back
          </Button>
        }
      />
    );
  }

  const steps = workflow.steps ?? [];
  const runCount = runs?.length ?? 0;
  const succeededRuns = (runs ?? []).filter((r: WorkflowRun) => r.status === "succeeded").length;
  const failedRuns = (runs ?? []).filter((r: WorkflowRun) => r.status === "failed").length;

  const tabs = [
    { id: "diagram", label: "Diagram", count: steps.length },
    { id: "runs", label: "Runs", count: runCount },
    { id: "config", label: "Configuration" },
    { id: "policy", label: "Policy" },
  ];

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-3">
          <button
            onClick={() => navigate("/workflows")}
            className="p-2 rounded-full hover:bg-surface-2 transition-colors"
          >
            <ArrowLeft className="w-4 h-4 text-muted-foreground" />
          </button>
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-xl bg-cordum/10 border border-cordum/20 flex items-center justify-center">
              <GitBranch className="w-5 h-5 text-cordum" />
            </div>
            <div>
              <div className="flex items-center gap-2">
                <h1 className="text-lg font-bold font-display text-foreground">{workflow.name}</h1>
                <StatusBadge variant="healthy">active</StatusBadge>
                <span className="text-xs font-mono text-muted-foreground px-1.5 py-0.5 rounded bg-surface-2">
                  v{workflow.version ?? 1}
                </span>
              </div>
              {workflow.description && (
                <p className="text-sm text-muted-foreground mt-0.5">{workflow.description}</p>
              )}
            </div>
          </div>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => navigate(`/workflows/${id}/edit`)}>
            <Edit className="w-3 h-3 mr-1" />
            Edit
          </Button>
          <Button
            variant="primary"
            size="sm"
            loading={startRun.isPending}
            onClick={() => {
              if (!id) return;
              startRun.mutate(
                { workflowId: id },
                {
                  onSuccess: (data) => {
                    toast.success("Workflow run started");
                    if (data?.run_id) navigate(`/workflows/${id}/runs/${data.run_id}`);
                  },
                  onError: () => toast.error("Failed to start workflow run"),
                },
              );
            }}
          >
            <Play className="w-3 h-3 mr-1" />
            Run
          </Button>
        </div>
      </div>

      {/* Quick Stats */}
      <div className="flex items-center gap-6 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5">
          <Layers className="w-3.5 h-3.5" />
          {steps.length} step{steps.length !== 1 ? "s" : ""}
        </span>
        <span className="flex items-center gap-1.5">
          <Play className="w-3.5 h-3.5" />
          {runCount} run{runCount !== 1 ? "s" : ""}
        </span>
        {succeededRuns > 0 && (
          <span className="flex items-center gap-1.5 text-[var(--color-success)]">
            <CheckCircle2 className="w-3.5 h-3.5" />
            {succeededRuns} succeeded
          </span>
        )}
        {failedRuns > 0 && (
          <span className="flex items-center gap-1.5 text-destructive">
            <XCircle className="w-3.5 h-3.5" />
            {failedRuns} failed
          </span>
        )}
        {latestRun?.startedAt && (
          <span className="flex items-center gap-1.5">
            <Clock className="w-3.5 h-3.5" />
            Last run {formatRelativeTime(latestRun.startedAt)}
          </span>
        )}
      </div>

      {/* Tabs */}
      <div className="flex items-center gap-1 bg-surface-1 border border-border rounded-2xl p-0.5 w-fit">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={cn(
              "px-4 py-1.5 text-xs font-medium rounded transition-colors",
              activeTab === tab.id
                ? "bg-cordum/10 text-cordum"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {tab.label}
            {tab.count !== undefined && tab.count > 0 && (
              <span className="ml-1.5 px-1.5 py-0.5 rounded-full text-[10px] font-mono bg-surface-2">
                {tab.count}
              </span>
            )}
          </button>
        ))}
      </div>

      {/* ============ DIAGRAM TAB ============ */}
      {activeTab === "diagram" && (
        steps.length === 0 ? (
          <EmptyState
            icon={<GitBranch className="w-5 h-5" />}
            title="No steps defined"
            description="Edit this workflow to add steps"
            action={
              <Button variant="outline" size="sm" onClick={() => navigate(`/workflows/${id}/edit`)}>
                <Edit className="w-3 h-3 mr-1" />
                Open Editor
              </Button>
            }
          />
        ) : (
          <>
            {/* Run overlay selector */}
            {runCount > 0 && (
              <div className="flex items-center gap-2">
                <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">
                  Overlay run:
                </span>
                <select
                  value={selectedRunId ?? ""}
                  onChange={(e) => setSelectedRunId(e.target.value || null)}
                  className="h-7 px-2 text-xs bg-surface-1 border border-border rounded-xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
                >
                  <option value="">Blueprint (no run)</option>
                  {(runs ?? []).map((r: WorkflowRun) => (
                    <option key={r.id} value={r.id}>
                      {r.id.slice(0, 12)} — {r.status}
                      {r.startedAt ? ` (${formatRelativeTime(r.startedAt)})` : ""}
                    </option>
                  ))}
                </select>
                {latestRun && !selectedRunId && (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-[10px] h-7"
                    onClick={() => setSelectedRunId(latestRun.id)}
                  >
                    Show latest
                  </Button>
                )}
              </div>
            )}

            {/* DAG */}
            <motion.div
              initial={{ opacity: 0, y: 12 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.3 }}
              className="instrument-card p-0 overflow-hidden"
              style={{ height: 520 }}
            >
              <RunDAG
                workflow={workflow}
                run={selectedRun}
                onNodeClick={handleNodeClick}
              />
            </motion.div>

            {/* Step detail panel — slides in from right */}
            <NodeDetailPanel
              step={selectedStep}
              run={selectedRun}
              onClose={() => setSelectedStep(null)}
            />
          </>
        )
      )}

      {/* ============ RUNS TAB ============ */}
      {activeTab === "runs" && (
        runCount === 0 ? (
          <EmptyState
            icon={<Play className="w-5 h-5" />}
            title="No runs yet"
            description="Run this workflow to see execution history"
            action={
              <Button
                variant="primary"
                size="sm"
                loading={startRun.isPending}
                onClick={() => {
                  if (!id) return;
                  startRun.mutate(
                    { workflowId: id },
                    {
                      onSuccess: (data) => {
                        toast.success("Workflow run started");
                        if (data?.run_id) navigate(`/workflows/${id}/runs/${data.run_id}`);
                      },
                      onError: () => toast.error("Failed to start workflow run"),
                    },
                  );
                }}
              >
                <Play className="w-3 h-3 mr-1" />
                Run Now
              </Button>
            }
          />
        ) : (
          <motion.div
            initial={{ opacity: 0, y: 12 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.3 }}
            className="instrument-card p-0 overflow-hidden"
          >
            <table className="w-full">
              <thead>
                <tr className="border-b border-border bg-surface-0">
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Status</th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Run ID</th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Started</th>
                  <th className="text-right px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Completed</th>
                  <th className="px-5 py-3 w-20 text-right text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Actions</th>
                </tr>
              </thead>
              <tbody>
                {(runs ?? []).map((r: WorkflowRun) => (
                  <tr
                    key={r.id}
                    {...clickableRowProps(() => navigate(`/workflows/${id}/runs/${r.id}`))}
                    className="border-b border-border hover:bg-surface-1 transition-colors cursor-pointer"
                  >
                    <td className="px-5 py-3">
                      <StatusBadge
                        variant={r.status === "succeeded" ? "healthy" : r.status === "running" ? "info" : r.status === "failed" ? "danger" : "muted"}
                        dot
                        pulse={r.status === "running"}
                      >
                        {r.status}
                      </StatusBadge>
                    </td>
                    <td className="px-5 py-3 font-mono text-sm text-cordum">{r.id.slice(0, 16)}</td>
                    <td className="px-5 py-3 text-xs text-muted-foreground font-mono">
                      {r.startedAt ? formatRelativeTime(r.startedAt) : "—"}
                    </td>
                    <td className="px-5 py-3 text-right text-xs text-muted-foreground font-mono">
                      {r.completedAt ? formatRelativeTime(r.completedAt) : "—"}
                    </td>
                    <td className="px-5 py-3 text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 text-[10px]"
                        onClick={(e) => {
                          e.stopPropagation();
                          setSelectedRunId(r.id);
                          setActiveTab("diagram");
                        }}
                      >
                        <Eye className="w-3 h-3 mr-1" />
                        View DAG
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </motion.div>
        )
      )}

      {/* ============ CONFIG TAB ============ */}
      {activeTab === "config" && (
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3 }}
          className="instrument-card"
        >
          <InstrumentCardHeader title="Workflow Configuration" icon={<Workflow className="w-4 h-4" />} />
          <div className="surface-inset p-4 font-mono text-xs text-foreground overflow-auto max-h-[400px]">
            <pre>{JSON.stringify(workflow, null, 2)}</pre>
          </div>
        </motion.div>
      )}

      {/* ============ POLICY TAB ============ */}
      {activeTab === "policy" && (
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3 }}
          className="space-y-4"
        >
          {constraintsDirty && (
            <div className="flex items-center justify-between rounded-2xl border border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10 px-3 py-2">
              <span className="text-xs text-[var(--color-warning)]">Unsaved constraint changes</span>
              <Button size="sm" loading={updateWorkflow.isPending} onClick={() => void saveConstraints()}>
                <Save className="w-3 h-3 mr-1" />
                Save Overrides
              </Button>
            </div>
          )}

          <WorkflowPolicyOverrides
            constraints={activeConstraints}
            readOnly={false}
            onChange={setConstraintDraft}
          />

          <WorkflowPolicyOverrideRules rules={workflowRules} />

          {/* Step-Level Overrides */}
          <div className="instrument-card">
            <InstrumentCardHeader
              title="Step-Level Overrides"
              subtitle="Each step inherits workflow-level constraints."
              icon={<Shield className="w-4 h-4" />}
            />
            {steps.length === 0 ? (
              <p className="text-xs text-muted-foreground">No steps defined in this workflow.</p>
            ) : (
              <div className="space-y-2 mt-1">
                {steps.map((step) => (
                  <div key={step.id} className="surface-inset p-3 flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <span className="text-xs font-mono px-2 py-0.5 rounded-full bg-surface-2 border border-border text-muted-foreground">
                        {step.type}
                      </span>
                      <span className="text-sm font-medium text-foreground">{step.name}</span>
                    </div>
                    <span className="text-[10px] font-mono text-muted-foreground">inherits workflow policy</span>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Global Policy Link */}
          <div className="instrument-card">
            <InstrumentCardHeader
              title="Global Policy"
              subtitle="Workflow constraints merge with global policy rules during evaluation."
              icon={<Shield className="w-4 h-4" />}
            />
            <div className="mt-1">
              <Button variant="outline" size="sm" onClick={() => navigate("/govern/input-rules")}>
                <Shield className="w-3 h-3 mr-1" />
                View Global Rules
              </Button>
            </div>
          </div>
        </motion.div>
      )}
    </div>
  );
}
