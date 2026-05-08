import { motion } from "framer-motion";
import type { ColumnDef } from "@tanstack/react-table";
import { ArrowRight } from "lucide-react";
import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { DataTable, type DecisionTier } from "@/components/primitives/DataTable";
import { SafetyDecisionFeed } from "@/components/home/SafetyDecisionFeed";
import { formatRelativeTime } from "@/lib/utils";
import type { Job } from "@/api/types";

interface RecentActivityListProps {
  jobs: Job[];
}

const RECENT_LIMIT = 5;

function statusVariant(status: Job["status"]): BadgeVariant {
  switch (status) {
    case "running":
    case "succeeded":
      return "healthy";
    case "failed":
    case "timeout":
      return "danger";
    case "denied":
    case "output_quarantined":
      return "governance";
    case "pending":
    case "scheduled":
    case "approval_required":
      return "warning";
    default:
      return "muted";
  }
}

function statusLabel(status: Job["status"]): string {
  return status === "output_quarantined" ? "quarantined" : status;
}

const decisionTier = (job: Job): DecisionTier | undefined =>
  job.safetyDecision?.type as DecisionTier | undefined;

export function RecentActivityList({ jobs }: RecentActivityListProps) {
  const navigate = useNavigate();
  const rows = useMemo(() => jobs.slice(0, RECENT_LIMIT), [jobs]);

  const columns = useMemo<ColumnDef<Job>[]>(
    () => [
      {
        id: "id",
        header: "Job ID",
        accessorFn: (job) => job.id,
        cell: ({ row }) => (
          <span className="font-mono text-cordum">{row.original.id.slice(0, 12)}</span>
        ),
        enableSorting: false,
      },
      {
        id: "topic",
        header: "Topic",
        accessorFn: (job) => job.topic ?? "—",
        cell: ({ row }) => (
          <span className="text-foreground">{row.original.topic || "—"}</span>
        ),
        enableSorting: false,
      },
      {
        id: "status",
        header: "Status",
        accessorFn: (job) => job.status,
        cell: ({ row }) => (
          <StatusBadge variant={statusVariant(row.original.status)}>
            {statusLabel(row.original.status)}
          </StatusBadge>
        ),
        enableSorting: false,
      },
      {
        id: "safety",
        header: "Safety",
        accessorFn: (job) => job.safetyDecision?.type ?? "",
        cell: ({ row }) => (
          <SafetyDecisionBadge decision={row.original.safetyDecision?.type} />
        ),
        enableSorting: false,
      },
      {
        id: "time",
        header: "Time",
        accessorFn: (job) => job.updatedAt,
        cell: ({ row }) => (
          <span className="text-muted-foreground">
            {row.original.updatedAt
              ? formatRelativeTime(new Date(row.original.updatedAt).toISOString())
              : "—"}
          </span>
        ),
        enableSorting: false,
      },
    ],
    [],
  );

  return (
    <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 mt-6">
      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3, delay: 0.2 }}
        className="instrument-card lg:col-span-8 overflow-hidden"
      >
        <div className="flex items-center justify-between px-5 py-3 border-b border-border/50">
          <h2 className="font-display font-semibold text-sm text-foreground">
            Recent Activity
          </h2>
          <Button variant="ghost" size="sm" onClick={() => navigate("/jobs")}>
            View all <ArrowRight className="w-3 h-3 ml-1" />
          </Button>
        </div>
        <DataTable
          columns={columns}
          data={rows}
          decisionAccessor={decisionTier}
          onRowClick={(job) => navigate(`/jobs/${job.id}`)}
          emptyState={<EmptyState title="No recent jobs" description="Run history will appear here." />}
          compact
        />
      </motion.div>

      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3, delay: 0.25 }}
        className="lg:col-span-4"
      >
        <SafetyDecisionFeed />
      </motion.div>
    </div>
  );
}
