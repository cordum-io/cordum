import { useParams } from "react-router-dom";
import { motion } from "framer-motion";
import { PageHeader } from "@/components/layout/PageHeader";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Skeleton } from "@/components/ui/Skeleton";
import { StatTile } from "@/components/ui/StatTile";
import { useEvalRun } from "@/hooks/useEvals";
import { EntryResultList } from "@/components/evals/EntryResultList";

export default function EvalRunDetailPage() {
  const { runId } = useParams<{ runId: string }>();
  const run = useEvalRun(runId);

  if (run.isLoading || !run.data) {
    return (
      <div className="p-6 space-y-4">
        <Skeleton className="h-10 w-72" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }

  if (run.isError) {
    return (
      <div className="p-6">
        <ErrorBanner
          title="Could not load eval run"
          message={run.error instanceof Error ? run.error.message : undefined}
          onRetry={() => run.refetch()}
        />
      </div>
    );
  }

  const r = run.data;
  const { summary } = r;
  const scoreLabel =
    summary.scorePercent === null ? "—" : `${Math.round(summary.scorePercent)}%`;

  return (
    <motion.div
      className="p-6 space-y-6"
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3 }}
    >
      <PageHeader
        title={`Run ${r.runId}`}
        subtitle={`Dataset ${r.datasetName} v${r.datasetVersion} · policy ${r.policySnapshot.slice(0, 12)}`}
        label="EVAL RUN"
      />
      <div className="grid grid-cols-2 md:grid-cols-5 gap-3">
        <StatTile label="Total" value={summary.total} />
        <StatTile label="Passed" value={summary.passed} />
        <StatTile label="Failed" value={summary.failed} />
        <StatTile label="Regressions" value={summary.regressions} />
        <StatTile label="Score" value={scoreLabel} />
      </div>
      <div className="instrument-card p-4">
        <EntryResultList run={r} />
      </div>
    </motion.div>
  );
}
