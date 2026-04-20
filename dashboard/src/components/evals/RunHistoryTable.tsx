import { useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/Button";
import { Badge } from "@/components/ui/Badge";
import { DataTable } from "@/components/ui/DataTable";
import { formatRelativeTime } from "@/lib/utils";
import type { EvalRun } from "@/api/types";

export interface RunHistoryTableProps {
  runs: EvalRun[];
  hasNextPage: boolean;
  isFetchingNextPage?: boolean;
  onLoadMore: () => void;
}

function scoreVariant(score: number | null): "success" | "warning" | "danger" | "default" {
  if (score === null) return "default";
  if (score >= 95) return "success";
  if (score >= 80) return "warning";
  return "danger";
}

function runStatus(run: EvalRun): { label: string; variant: "success" | "warning" | "danger" | "default" } {
  if ((run.summary.regressions ?? 0) > 0) return { label: "regression", variant: "danger" };
  if (run.summary.errored > 0) return { label: "error", variant: "warning" };
  if (run.summary.failed > 0) return { label: "fail", variant: "warning" };
  if (!run.completedAt) return { label: "running", variant: "default" };
  return { label: "pass", variant: "success" };
}

export function RunHistoryTable({
  runs,
  hasNextPage,
  isFetchingNextPage,
  onLoadMore,
}: RunHistoryTableProps) {
  const navigate = useNavigate();
  const columns = [
    {
      key: "startedAt",
      header: "Started",
      width: "10rem",
      render: (r: EvalRun) => (
        <span className="text-xs text-muted-foreground">
          {r.startedAt ? formatRelativeTime(r.startedAt) : "—"}
        </span>
      ),
    },
    {
      key: "score",
      header: "Score",
      width: "6rem",
      render: (r: EvalRun) => {
        const s = r.summary.scorePercent;
        return (
          <Badge variant={scoreVariant(s)} aria-label={`score ${s === null ? "unknown" : s}`}>
            {s === null ? "—" : `${Math.round(s)}%`}
          </Badge>
        );
      },
    },
    { key: "passed", header: "Passed", width: "5rem", align: "right" as const, render: (r: EvalRun) => r.summary.passed },
    { key: "failed", header: "Failed", width: "5rem", align: "right" as const, render: (r: EvalRun) => r.summary.failed },
    {
      key: "regressions",
      header: "Regressions",
      width: "6rem",
      align: "right" as const,
      render: (r: EvalRun) => r.summary.regressions,
    },
    {
      key: "status",
      header: "Status",
      width: "7rem",
      render: (r: EvalRun) => {
        const s = runStatus(r);
        return (
          <Badge variant={s.variant} aria-label={`run status ${s.label}`}>
            {s.label}
          </Badge>
        );
      },
    },
  ];

  return (
    <div className="space-y-3">
      <DataTable
        columns={columns}
        data={runs}
        keyExtractor={(r) => r.runId}
        emptyMessage="No runs yet."
        onRowClick={(r) => navigate(`/evals/runs/${encodeURIComponent(r.runId)}`)}
      />
      {hasNextPage && (
        <div className="flex justify-center">
          <Button variant="outline" size="sm" loading={isFetchingNextPage} onClick={onLoadMore}>
            Load more
          </Button>
        </div>
      )}
    </div>
  );
}
