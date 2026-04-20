import { useMemo, useState } from "react";
import { FlaskConical } from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { Tabs } from "@/components/ui/Tabs";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Button } from "@/components/ui/Button";
import { Skeleton } from "@/components/ui/Skeleton";
import { useEvalDatasets } from "@/hooks/useEvals";
import { DatasetList } from "@/components/evals/DatasetList";
import { IncidentExtractionDialog } from "@/components/evals/IncidentExtractionDialog";

type TabId = "datasets" | "runs";

export default function EvalsPage() {
  const [activeTab, setActiveTab] = useState<TabId>("datasets");
  const [extractionOpen, setExtractionOpen] = useState(false);
  const datasets = useEvalDatasets();

  const flatDatasets = useMemo(
    () => datasets.data?.pages.flatMap((p) => p.items) ?? [],
    [datasets.data],
  );

  const tabs = [
    { id: "datasets", label: "Datasets", count: flatDatasets.length || undefined },
    { id: "runs", label: "Run History" },
  ];

  const renderDatasetsTab = () => {
    if (datasets.isLoading) {
      return (
        <div className="grid gap-3">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      );
    }
    if (datasets.isError) {
      return (
        <ErrorBanner
          title="Could not load eval datasets"
          message={datasets.error instanceof Error ? datasets.error.message : undefined}
          onRetry={() => datasets.refetch()}
        />
      );
    }
    if (flatDatasets.length === 0) {
      return (
        <EmptyState
          icon={<FlaskConical className="w-5 h-5" />}
          title="No eval datasets yet"
          description="Denied incidents become test cases. Build a regression suite by extracting from your audit trail."
          action={
            <Button variant="primary" onClick={() => setExtractionOpen(true)}>
              Create dataset from incidents
            </Button>
          }
        />
      );
    }
    return (
      <DatasetList
        datasets={flatDatasets}
        hasNextPage={datasets.hasNextPage ?? false}
        isFetchingNextPage={datasets.isFetchingNextPage}
        onLoadMore={() => datasets.fetchNextPage()}
        onCreateFromIncidents={() => setExtractionOpen(true)}
      />
    );
  };

  const renderRunsTab = () => (
    <EmptyState
      icon={<FlaskConical className="w-5 h-5" />}
      title="Run history lives on each dataset"
      description="Open a dataset to see its score trend, regressions, and per-entry drill-down."
    />
  );

  return (
    <div className="space-y-6 p-6">
      <PageHeader
        title="Evaluations"
        subtitle="Policy regression suites from denied actions"
        label="GOVERN"
      />
      <Tabs
        tabs={tabs}
        activeTab={activeTab}
        onChange={(id) => setActiveTab(id as TabId)}
        ariaLabel="Eval views"
      />
      <div>{activeTab === "datasets" ? renderDatasetsTab() : renderRunsTab()}</div>
      <IncidentExtractionDialog
        open={extractionOpen}
        onOpenChange={setExtractionOpen}
      />
    </div>
  );
}
