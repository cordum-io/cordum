import { Link } from "react-router-dom";
import { Upload } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Badge } from "@/components/ui/Badge";
import { LegacyDataTable } from "@/components/ui/LegacyDataTable";
import { formatRelativeTime } from "@/lib/utils";
import type { EvalDataset, EvalRun } from "@/api/types";

export interface DatasetListEntry {
  dataset: EvalDataset;
  latestRun?: EvalRun;
}

export interface DatasetListProps {
  datasets: EvalDataset[];
  latestRunsByDatasetId?: Record<string, EvalRun | undefined>;
  hasNextPage: boolean;
  isFetchingNextPage?: boolean;
  onLoadMore: () => void;
  onCreateFromIncidents: () => void;
}

function scoreBadgeVariant(score: number | null | undefined): "success" | "warning" | "danger" | "default" {
  if (score === null || score === undefined) return "default";
  if (score >= 95) return "success";
  if (score >= 80) return "warning";
  return "danger";
}

function ScoreBadge({ score }: { score: number | null | undefined }) {
  const variant = scoreBadgeVariant(score);
  const label = score === null || score === undefined ? "—" : `${Math.round(score)}%`;
  return (
    <Badge variant={variant} aria-label={`last run score ${label}`}>
      {label}
    </Badge>
  );
}

export function DatasetList({
  datasets,
  latestRunsByDatasetId,
  hasNextPage,
  isFetchingNextPage,
  onLoadMore,
  onCreateFromIncidents,
}: DatasetListProps) {
  const columns = [
    {
      key: "name",
      header: "Name",
      render: (ds: EvalDataset) => (
        <Link
          to={`/evals/${encodeURIComponent(ds.id)}`}
          className="font-semibold text-foreground hover:text-cordum"
        >
          {ds.name}
        </Link>
      ),
    },
    {
      key: "version",
      header: "Version",
      width: "6rem",
      render: (ds: EvalDataset) => (
        <span className="font-mono text-xs text-muted-foreground">v{ds.version}</span>
      ),
    },
    {
      key: "entryCount",
      header: "Entries",
      width: "6rem",
      align: "right" as const,
      render: (ds: EvalDataset) => (
        <span className="font-mono text-xs">{ds.entryCount}</span>
      ),
    },
    {
      key: "lastScore",
      header: "Last run",
      width: "8rem",
      render: (ds: EvalDataset) => {
        const run = latestRunsByDatasetId?.[ds.id];
        return <ScoreBadge score={run?.summary.scorePercent} />;
      },
    },
    {
      key: "lastRunAt",
      header: "When",
      width: "10rem",
      render: (ds: EvalDataset) => {
        const run = latestRunsByDatasetId?.[ds.id];
        if (!run?.startedAt) return <span className="text-xs text-muted-foreground">—</span>;
        return (
          <span className="text-xs text-muted-foreground">
            {formatRelativeTime(run.startedAt)}
          </span>
        );
      },
    },
    {
      key: "regression",
      header: "Regression",
      width: "6rem",
      align: "center" as const,
      render: (ds: EvalDataset) => {
        const run = latestRunsByDatasetId?.[ds.id];
        if (!run || (run.summary.regressions ?? 0) === 0) {
          return <span className="sr-only">no regressions</span>;
        }
        return (
          <span
            className="inline-block h-2.5 w-2.5 rounded-full bg-danger"
            aria-label={`${run.summary.regressions} regression(s) in latest run`}
            title={`${run.summary.regressions} regression(s) in latest run`}
          />
        );
      },
    },
  ];

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-end gap-2">
        <Button
          variant="ghost"
          disabled
          title="Coming soon"
          aria-label="Upload dataset (coming soon)"
        >
          <Upload className="w-3.5 h-3.5" />
          Upload dataset
        </Button>
        <Button variant="default" onClick={onCreateFromIncidents}>
          Create from incidents
        </Button>
      </div>
      <LegacyDataTable
        columns={columns}
        data={datasets}
        keyExtractor={(ds) => ds.id}
        emptyMessage="No datasets yet."
      />
      {hasNextPage && (
        <div className="flex justify-center">
          <Button
            variant="outline"
            size="sm"
            loading={isFetchingNextPage}
            onClick={onLoadMore}
          >
            Load more
          </Button>
        </div>
      )}
    </div>
  );
}
