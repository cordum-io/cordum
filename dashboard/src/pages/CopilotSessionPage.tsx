import { useNavigate, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { ArrowLeft } from "lucide-react";
import { get } from "@/api/client";
import { mapJobRecord, type BackendJobRecord } from "@/api/transform";
import type { Job } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { SkeletonTable } from "@/components/ui/Skeleton";
import { PageHeader } from "@/components/layout/PageHeader";
import { clickableRowProps, formatRelativeTime } from "@/lib/utils";

const PAGE_LIMIT = 50;

function jobStatusVariant(status: string) {
  switch (status) {
    case "running":
    case "succeeded":
      return "healthy" as const;
    case "failed":
    case "failed_fatal":
      return "danger" as const;
    case "denied":
      return "governance" as const;
    case "failed_retryable":
    case "pending":
    case "scheduled":
      return "warning" as const;
    case "dispatched":
      return "info" as const;
    default:
      return "muted" as const;
  }
}

export default function CopilotSessionPage() {
  const navigate = useNavigate();
  const { sessionId } = useParams<{ sessionId: string }>();
  const trimmed = (sessionId ?? "").trim();

  if (!trimmed) {
    return (
      <motion.div
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        className="space-y-6"
      >
        <EmptyState
          title="Missing session id"
          description="The URL did not include a Copilot Session id. Open a session from the Jobs page to view its detail."
        />
        <div className="flex justify-center">
          <Button variant="outline" size="sm" onClick={() => navigate("/jobs")}>
            <ArrowLeft className="w-3 h-3 mr-1" />
            Back to Jobs
          </Button>
        </div>
      </motion.div>
    );
  }

  const { data, isLoading, isError, error, refetch } = useQuery<Job[], Error>({
    queryKey: ["copilot-session-jobs", trimmed],
    queryFn: async () => {
      const res = await get<{ items?: BackendJobRecord[] }>(
        `/jobs?session_id=${encodeURIComponent(trimmed)}&limit=${PAGE_LIMIT}`,
      );
      return (res.items ?? [])
        .map(mapJobRecord)
        .filter((j): j is Job => !!j);
    },
    enabled: !!trimmed,
    staleTime: 10_000,
  });

  const jobs = data ?? [];

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      className="space-y-6"
    >
      <PageHeader
        label="Copilot Session"
        title={trimmed}
        subtitle="Linked jobs for this Copilot session."
        actions={
          <Button variant="outline" size="sm" onClick={() => navigate("/jobs")}>
            <ArrowLeft className="w-3 h-3 mr-1" />
            Back to Jobs
          </Button>
        }
      />

      <div
        data-testid="copilot-session-roadmap-banner"
        className="instrument-card border-dashed border-cordum/30 bg-cordum/5"
      >
        <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest mb-1">
          Roadmap
        </p>
        <p className="text-sm text-foreground">
          The full Copilot Session timeline (messages, governance decisions,
          per-turn job chips) is coming soon. For now, this page shows the
          session id and the jobs the session has produced. Tracked in
          task-da7eaef7.
        </p>
      </div>

      {isError ? (
        <ErrorBanner
          message={error instanceof Error ? error.message : "Failed to load linked jobs"}
          onRetry={() => void refetch()}
        />
      ) : isLoading ? (
        <SkeletonTable rows={5} />
      ) : jobs.length === 0 ? (
        <EmptyState
          title="No jobs yet"
          description="No jobs linked to this session yet."
        />
      ) : (
        <div className="instrument-card overflow-hidden p-0">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border">
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                  Job ID
                </th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                  Topic
                </th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                  Status
                </th>
                <th className="text-right px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                  Updated
                </th>
              </tr>
            </thead>
            <tbody>
              {jobs.map((job) => (
                <tr
                  key={job.id}
                  {...clickableRowProps(() => navigate(`/jobs/${job.id}`))}
                  className="border-b border-border last:border-0 hover:bg-surface-1 transition-colors cursor-pointer"
                >
                  <td className="px-5 py-3 font-mono text-sm text-cordum">
                    {job.id.slice(0, 16)}
                  </td>
                  <td className="px-5 py-3 text-foreground">{job.topic ?? "—"}</td>
                  <td className="px-5 py-3">
                    <StatusBadge variant={jobStatusVariant(job.status)} dot>
                      {job.status}
                    </StatusBadge>
                  </td>
                  <td className="px-5 py-3 text-right text-xs text-muted-foreground font-mono">
                    {job.updatedAt
                      ? formatRelativeTime(new Date(job.updatedAt).toISOString())
                      : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {jobs.length === PAGE_LIMIT && (
            <p className="px-5 py-2 text-xs text-muted-foreground font-mono border-t border-border">
              Showing first {PAGE_LIMIT} jobs.
            </p>
          )}
        </div>
      )}
    </motion.div>
  );
}
