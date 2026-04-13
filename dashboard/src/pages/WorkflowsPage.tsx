/*
 * DESIGN: "Control Surface" — Workflows
 * Matches cordumds-gj5mw4zm.manus.space showcase patterns
 */
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { get } from "@/api/client";
import { PageHeader } from "@/components/layout/PageHeader";
import { TierLimitBar } from "@/components/TierLimitBar";
import { UpgradePrompt } from "@/components/UpgradePrompt";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonTable } from "@/components/ui/Skeleton";
import { Search, Plus, Workflow, RefreshCw, Eye, GitBranch } from "lucide-react";
import { formatRelativeTime, clickableRowProps } from "@/lib/utils";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { useLicenseUsage } from "@/hooks/useLicense";

interface WorkflowSummary {
  id: string;
  name: string;
  description?: string;
  version?: number;
  status?: string;
  stepCount?: number;
  lastRunAt?: string;
  createdAt?: string;
  updatedAt?: string;
}

export default function WorkflowsPage() {
  const navigate = useNavigate();
  const [search, setSearch] = useState("");
  const { data: licenseUsage } = useLicenseUsage();

  const { data: workflows, isLoading, isError, error, refetch } = useQuery({
    queryKey: ["workflows"],
    queryFn: async () => {
      const res = await get<WorkflowSummary[] | { items: WorkflowSummary[] }>("/workflows?limit=200");
      // Backend may return a bare array or { items: [...] }
      const items = Array.isArray(res) ? res : (res.items ?? []);
      return items.map((w) => {
        const raw = w as unknown as Record<string, unknown>;
        const steps = raw.steps as Record<string, unknown> | undefined;
        return {
          ...w,
          stepCount: w.stepCount ?? (steps ? Object.keys(steps).length : undefined),
          lastRunAt: w.lastRunAt ?? raw.last_run_at as string | undefined,
          createdAt: w.createdAt ?? raw.created_at as string | undefined,
          updatedAt: w.updatedAt ?? raw.updated_at as string | undefined,
        };
      });
    },
    refetchInterval: 30_000,
  });

  const all = workflows ?? [];
  const filtered = all.filter((w) => {
    if (!search) return true;
    const q = search.toLowerCase();
    return w.name.toLowerCase().includes(q) || w.id.toLowerCase().includes(q) || (w.description ?? "").toLowerCase().includes(q);
  });

  if (isError) {
    return <ErrorBanner message={error instanceof Error ? error.message : "Failed to load workflows"} onRetry={() => void refetch()} />;
  }

  const activeWorkflowMetric = licenseUsage?.usage?.activeWorkflows;
  const workflowStepMetric = licenseUsage?.usage?.workflowSteps;

  return (
    <div className="space-y-6">
      <PageHeader
        label="Core"
        title="Workflows"
        subtitle={`${all.length} workflow${all.length !== 1 ? "s" : ""} defined`}
        actions={
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              <RefreshCw className="w-3 h-3 mr-1" />
              Refresh
            </Button>
            <Button variant="primary" size="sm" onClick={() => navigate("/workflows/studio/new")}>
              <Plus className="w-3 h-3 mr-1" />
              New Workflow
            </Button>
          </div>
        }
      />

      {(activeWorkflowMetric || workflowStepMetric) && (
        <div className="space-y-3">
          <div className="grid gap-4 xl:grid-cols-2">
            {activeWorkflowMetric && (
              <TierLimitBar
                label="Active workflows"
                metric={activeWorkflowMetric}
                detail="Concurrent workflow runs count against this ceiling."
              />
            )}
            {workflowStepMetric && (
              <TierLimitBar
                label="Workflow steps / run"
                metric={workflowStepMetric}
                detail={`Approval mode: ${(licenseUsage?.usage?.approvalMode.allowed ?? "—").toString()}`}
              />
            )}
          </div>
          <UpgradePrompt label="Active workflows" metric={activeWorkflowMetric} plan={licenseUsage?.plan} />
        </div>
      )}

      {/* Search — showcase style */}
      <div className="relative max-w-sm">
        <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
        <input
          type="text"
          placeholder="Search workflows..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="h-8 w-full pl-8 pr-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
        />
      </div>

      {/* Workflows Table — showcase style */}
      {isLoading ? (
        <div className="instrument-card">
          <SkeletonTable rows={6} />
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<Workflow className="w-5 h-5" />}
          title="No workflows found"
          description={search ? "Try adjusting your search" : "Create your first workflow to orchestrate agent tasks"}
          action={
            <Button variant="primary" size="sm" onClick={() => navigate("/workflows/studio/new")}>
              <Plus className="w-3 h-3 mr-1" />
              New Workflow
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
          <div className="overflow-x-auto">
          <table className="w-full min-w-[700px]">
            <thead>
              <tr className="border-b border-border bg-surface-0">
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Name</th>
                <th className="text-center px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Version</th>
                <th className="text-center px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Steps</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Status</th>
                <th className="text-right px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Last Run</th>
                <th className="px-5 py-3"></th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((w) => (
                <tr
                  key={w.id}
                  {...clickableRowProps(() => navigate(`/workflows/${w.id}/studio`))}
                  className="border-b border-border hover:bg-surface-1 transition-colors cursor-pointer"
                >
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-3">
                      <div className="w-8 h-8 rounded-2xl bg-cordum/10 border border-cordum/20 flex items-center justify-center shrink-0">
                        <GitBranch className="w-4 h-4 text-cordum" />
                      </div>
                      <div>
                        <p className="text-sm font-medium text-foreground">{w.name}</p>
                        {w.description && <p className="text-xs text-muted-foreground truncate max-w-[300px]">{w.description}</p>}
                      </div>
                    </div>
                  </td>
                  <td className="px-5 py-3 text-center font-mono text-xs text-muted-foreground">v{w.version ?? 1}</td>
                  <td className="px-5 py-3 text-center font-mono text-xs text-muted-foreground">{w.stepCount ?? "—"}</td>
                  <td className="px-5 py-3">
                    <StatusBadge variant={w.status === "active" ? "healthy" : w.status === "draft" ? "muted" : "warning"}>
                      {w.status ?? "active"}
                    </StatusBadge>
                  </td>
                  <td className="px-5 py-3 text-right text-xs text-muted-foreground font-mono">
                    {w.lastRunAt ? formatRelativeTime(w.lastRunAt) : "Never"}
                  </td>
                  <td className="px-5 py-3">
                    <button type="button" className="p-1 rounded hover:bg-surface-2 transition-colors" aria-label="View details">
                      <Eye className="w-3.5 h-3.5 text-muted-foreground" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
        </motion.div>
      )}
    </div>
  );
}
