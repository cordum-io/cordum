import { useState, useCallback, useEffect, useMemo } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { get } from "@/api/client";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { Skeleton } from "@/components/ui/Skeleton";
import { ArrowLeft, Play, Edit, GitBranch, Workflow, Eye, Shield } from "lucide-react";
import { useState } from "react";
import { cn, formatRelativeTime, clickableRowProps } from "@/lib/utils";
import { useStartRun } from "@/hooks/useWorkflows";
import { useRunStream } from "@/hooks/useRunStream";
import { toast } from "sonner";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function timeAgo(iso?: string): string {
  if (!iso) return "\u2014";
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1_000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

export default function WorkflowDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [activeTab, setActiveTab] = useState("steps");
  const startRun = useStartRun();

  // Subscribe to WebSocket run events — patches React Query cache for instant status updates
  useRunStream(null);

function truncate(str: string, max: number): string {
  return str.length > max ? str.slice(0, max) + "\u2026" : str;
}

// ---------------------------------------------------------------------------
// Steps mini-bar
// ---------------------------------------------------------------------------

const STEP_STATUS_COLORS: Record<string, string> = {
  succeeded: "bg-green-500",
  completed: "bg-green-500",
  running: "bg-blue-500",
  in_progress: "bg-blue-500",
  failed: "bg-red-500",
  timed_out: "bg-red-500",
  waiting: "bg-amber-500",
  blocked: "bg-amber-500",
  pending: "bg-gray-300",
  queued: "bg-gray-300",
  cancelled: "bg-gray-400",
};

function StepsMiniBar({ steps }: { steps: WorkflowStep[] }) {
  if (steps.length === 0) return <span className="text-xs text-muted">\u2014</span>;
  const total = steps.length;

  return (
    <div className="space-y-6">
      {/* Header — showcase style */}
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-3">
          <button
            onClick={() => navigate("/workflows")}
            className="p-2 rounded-md hover:bg-surface-2 transition-colors"
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
                <StatusBadge variant={workflow.status === "active" ? "healthy" : "muted"}>
                  {workflow.status ?? "active"}
                </StatusBadge>
                <span className="text-xs font-mono text-muted-foreground px-1.5 py-0.5 rounded bg-surface-2">v{workflow.version ?? 1}</span>
              </div>
              {workflow.description && <p className="text-sm text-muted-foreground mt-0.5">{workflow.description}</p>}
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
            onClick={() => startRun.mutate({ workflowId: id! }, {
              onSuccess: (data) => {
                toast.success("Workflow run started");
                if (data?.run_id) navigate(`/workflows/${id}/runs/${data.run_id}`);
              },
              onError: () => toast.error("Failed to start workflow run"),
            })}
          >
            <Play className="w-3 h-3 mr-1" />
            Run
          </Button>
        </div>
      </div>

      {/* Tabs — showcase style */}
      <div className="flex items-center gap-1 bg-surface-1 border border-border rounded-md p-0.5 w-fit">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={cn(
              "h-full",
              STEP_STATUS_COLORS[s.status ?? ""] ?? "bg-gray-200",
            )}
            style={{ width: `${100 / total}%` }}
          />
        ))}
      </div>

      {/* Steps Tab */}
      {activeTab === "steps" && (
        (workflow.steps?.length ?? 0) === 0 ? (
          <EmptyState
            icon={<GitBranch className="w-5 h-5" />}
            title="No steps defined"
            description="Edit this workflow to add steps"
          />
        ) : (
          <motion.div
            initial={{ opacity: 0, y: 12 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.3 }}
            className="instrument-card overflow-hidden"
          >
            <table className="w-full">
              <thead>
                <tr className="border-b border-border bg-surface-0">
                  <th className="text-center px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider w-12">#</th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Step Name</th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider w-24">Type</th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Topic</th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Depends On</th>
                </tr>
              </thead>
              <tbody>
                {(workflow.steps ?? []).map((s, i) => (
                  <tr key={s.id} className="border-b border-border hover:bg-surface-1 transition-colors">
                    <td className="px-5 py-3 text-center font-mono text-xs text-muted-foreground">{i + 1}</td>
                    <td className="px-5 py-3 text-sm font-medium text-foreground">{s.name}</td>
                    <td className="px-5 py-3">
                      <span className="text-xs font-mono px-2 py-0.5 rounded-full bg-surface-2 border border-border text-muted-foreground">{s.type}</span>
                    </td>
                    <td className="px-5 py-3 font-mono text-xs text-muted-foreground">{s.topic ?? "—"}</td>
                    <td className="px-5 py-3">
                      <div className="flex gap-1">
                        {(s.dependsOn ?? []).map((d) => (
                          <span key={d} className="text-[10px] font-mono px-1.5 py-0.5 rounded-full bg-cordum/10 text-cordum border border-cordum/20">{d}</span>
                        ))}
                        {(!s.dependsOn || s.dependsOn.length === 0) && <span className="text-xs text-muted-foreground">—</span>}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </motion.div>
        )
      )}

      {/* Runs Tab */}
      {activeTab === "runs" && (
        (workflow.runs?.length ?? 0) === 0 ? (
          <EmptyState
            icon={<Play className="w-5 h-5" />}
            title="No runs yet"
            description="Run this workflow to see execution history"
            action={
              <Button
                variant="primary"
                size="sm"
                loading={startRun.isPending}
                onClick={() => startRun.mutate({ workflowId: id! }, {
                  onSuccess: (data) => {
                    toast.success("Workflow run started");
                    if (data?.run_id) navigate(`/workflows/${id}/runs/${data.run_id}`);
                  },
                  onError: () => toast.error("Failed to start workflow run"),
                })}
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
            className="instrument-card overflow-hidden"
          >
            <table className="w-full">
              <thead>
                <tr className="border-b border-border bg-surface-0">
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Status</th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Run ID</th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Started</th>
                  <th className="text-right px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Completed</th>
                  <th className="px-5 py-3 w-10"></th>
                </tr>
              </thead>
              <tbody>
                {(workflow.runs ?? []).map((r) => (
                  <tr
                    key={r.id}
                    {...clickableRowProps(() => navigate(`/workflows/${id}/runs/${r.id}`))}
                    className="border-b border-border hover:bg-surface-1 transition-colors cursor-pointer"
                  >
                    <td className="px-5 py-3">
                      <StatusBadge
                        variant={r.status === "completed" ? "healthy" : r.status === "running" ? "info" : r.status === "failed" ? "danger" : "muted"}
                        dot
                        pulse={r.status === "running"}
                      >
                        {r.status}
                      </StatusBadge>
                    </td>
                    <td className="px-5 py-3 font-mono text-sm text-cordum">{r.id.slice(0, 16)}</td>
                    <td className="px-5 py-3 text-xs text-muted-foreground font-mono">{r.startedAt ? formatRelativeTime(r.startedAt) : "—"}</td>
                    <td className="px-5 py-3 text-right text-xs text-muted-foreground font-mono">{r.completedAt ? formatRelativeTime(r.completedAt) : "—"}</td>
                    <td className="px-5 py-3">
                      <button className="p-1 rounded hover:bg-surface-2 transition-colors">
                        <Eye className="w-3.5 h-3.5 text-muted-foreground" />
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </motion.div>
        )
      )}

      {/* Config Tab */}
      {activeTab === "config" && (
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3 }}
          className="instrument-card p-5"
        >
          <h3 className="font-display font-semibold text-sm text-foreground mb-3">Workflow Configuration</h3>
          <div className="rounded-md bg-surface-0 border border-border p-4 font-mono text-xs text-foreground overflow-auto max-h-[400px]">
            <pre>{JSON.stringify(workflow, null, 2)}</pre>
          </div>
        </motion.div>
      )}

      {/* Policy Tab */}
      {activeTab === "policy" && (
        <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.3 }} className="space-y-4">
          {/* Bound Bundles */}
          <div className="instrument-card p-5">
            <div className="flex items-center gap-2 mb-4">
              <Shield className="w-4 h-4 text-cordum" />
              <h3 className="font-display font-semibold text-sm text-foreground">Policy Bindings</h3>
            </div>
            <p className="text-xs text-muted-foreground mb-4">
              Policy bundles bound to this workflow. Rules in these bundles are evaluated for every job dispatched by this workflow.
            </p>
            <EmptyState
              icon={<Shield className="w-5 h-5" />}
              title="No policy bindings available"
              description="Workflow-level policy binding is not yet supported. Policies are evaluated globally for all jobs."
              action={
                <Button variant="outline" size="sm" onClick={() => navigate("/policies")}>
                  <Shield className="w-3 h-3 mr-1" />View Policies
                </Button>
              }
            />
          </div>

          {/* Step-Level Overrides */}
          <div className="instrument-card p-5">
            <div className="flex items-center gap-2 mb-4">
              <Shield className="w-4 h-4 text-cordum" />
              <h3 className="font-display font-semibold text-sm text-foreground">Step-Level Overrides</h3>
            </div>
            <p className="text-xs text-muted-foreground mb-4">
              Override policy rules for specific steps in this workflow.
            </p>
            {(workflow.steps?.length ?? 0) === 0 ? (
              <p className="text-xs text-muted-foreground">No steps defined in this workflow.</p>
            ) : (
              <div className="space-y-2">
                {(workflow.steps ?? []).map((step) => (
                  <div key={step.id} className="rounded-lg bg-surface-0 border border-border p-3 flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <span className="text-xs font-mono px-2 py-0.5 rounded-full bg-surface-2 border border-border text-muted-foreground">{step.type}</span>
                      <span className="text-sm font-medium text-foreground">{step.name}</span>
                    </div>
                    <span className="text-[10px] font-mono text-muted-foreground">inherits workflow policy</span>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Evaluation Summary */}
          <div className="instrument-card p-5">
            <div className="flex items-center gap-2 mb-4">
              <Shield className="w-4 h-4 text-cordum" />
              <h3 className="font-display font-semibold text-sm text-foreground">Evaluation Summary</h3>
            </div>
            <p className="text-xs text-muted-foreground">
              No evaluation data available. Per-workflow policy statistics will appear here once workflow-scoped evaluation tracking is enabled.
            </p>
          </div>
        </motion.div>
      )}
    </div>
  );
}
