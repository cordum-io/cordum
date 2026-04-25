import { useParams } from "react-router-dom";
import { motion } from "framer-motion";
import { PageHeader } from "@/components/layout/PageHeader";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Skeleton } from "@/components/ui/Skeleton";
import { StatTile } from "@/components/ui/StatTile";
import { Button } from "@/components/ui/Button";
import { useEvalDataset, useEvalRuns, useRunEvalDataset } from "@/hooks/useEvals";
import { RegressionBanner } from "@/components/evals/RegressionBanner";
import { ScoreTrendChart } from "@/components/evals/ScoreTrendChart";
import { RunHistoryTable } from "@/components/evals/RunHistoryTable";
import { useMemo } from "react";

export default function EvalDatasetDetailPage() {
  const { datasetId } = useParams<{ datasetId: string }>();
  const dataset = useEvalDataset(datasetId);
  const runs = useEvalRuns(datasetId);
  const runMutation = useRunEvalDataset(datasetId);

  const flatRuns = useMemo(
    () => runs.data?.pages.flatMap((p) => p.items) ?? [],
    [runs.data],
  );
  const latest = flatRuns[0];
  const lastScore = latest?.summary.scorePercent ?? null;

  if (dataset.isLoading) {
    return (
      <div className="p-6 space-y-4">
        <Skeleton className="h-10 w-72" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }

  if (dataset.isError || !dataset.data) {
    return (
      <div className="p-6">
        <ErrorBanner
          title="Could not load dataset"
          message={dataset.error instanceof Error ? dataset.error.message : undefined}
          onRetry={() => dataset.refetch()}
        />
      </div>
    );
  }

  const ds = dataset.data;

  return (
    <motion.div
      className="p-6 space-y-6"
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3 }}
    >
      <PageHeader
        title={ds.name}
        subtitle={ds.description || `Version ${ds.version} · ${ds.entryCount} entries`}
        label="EVAL DATASET"
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="primary"
              loading={runMutation.isPending}
              onClick={() => runMutation.mutate({ useCurrentPolicy: true })}
            >
              Run against current policy
            </Button>
          </div>
        }
      />

      <div className="grid grid-cols-2 md:grid-cols-3 gap-3">
        <StatTile label="Entries" value={ds.entryCount} />
        <StatTile
          label="Last score"
          value={lastScore === null ? "—" : `${Math.round(lastScore)}%`}
        />
        <StatTile label="Total runs" value={flatRuns.length} />
      </div>

      {latest && <RegressionBanner run={latest} />}

      <div className="instrument-card p-4">
        <ScoreTrendChart runs={flatRuns} />
      </div>

      <RunHistoryTable
        runs={flatRuns}
        hasNextPage={runs.hasNextPage ?? false}
        isFetchingNextPage={runs.isFetchingNextPage}
        onLoadMore={() => runs.fetchNextPage()}
      />
    </motion.div>
  );
}
