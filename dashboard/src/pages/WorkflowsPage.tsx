import { useState, useMemo, useEffect } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Plus, Search, Loader, Workflow } from "lucide-react";
import { useWorkflows, useStartRun } from "../hooks/useWorkflows";
import { ActiveRunsStrip } from "../components/workflows/ActiveRunsStrip";
import { WorkflowTemplateCard } from "../components/workflows/WorkflowTemplateCard";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { Input } from "../components/ui/Input";
import { EmptyState } from "../components/ui/EmptyState";
import { DataFreshness } from "../components/ui/DataFreshness";
import { usePageTitle } from "../hooks/usePageTitle";

// ---------------------------------------------------------------------------
// Skeleton cards
// ---------------------------------------------------------------------------

function SkeletonCards({ count = 6 }: { count?: number }) {
  return (
    <>
      {Array.from({ length: count }, (_, i) => (
        <Card key={i} className="animate-pulse">
          <div className="space-y-3">
            <div className="h-5 w-2/3 rounded bg-surface2" />
            <div className="flex gap-4">
              <div className="h-4 w-16 rounded bg-surface2" />
              <div className="h-4 w-20 rounded bg-surface2" />
              <div className="h-4 w-14 rounded bg-surface2" />
            </div>
            <div className="h-4 w-1/2 rounded bg-surface2" />
          </div>
        </Card>
      ))}
    </>
  );
}

// ---------------------------------------------------------------------------
// WorkflowsPage
// ---------------------------------------------------------------------------

export default function WorkflowsPage() {
  usePageTitle("Workflows");
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const { data: workflows, isLoading, isError, dataUpdatedAt, refetch, isRefetching } = useWorkflows();
  const startRun = useStartRun();

  // URL-persisted search
  const urlSearch = searchParams.get("q") ?? "";
  const [searchInput, setSearchInput] = useState(urlSearch);

  // Debounce write-back to URL (300ms)
  useEffect(() => {
    const id = setTimeout(() => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        if (searchInput.trim()) next.set("q", searchInput.trim());
        else next.delete("q");
        return next;
      }, { replace: true });
    }, 300);
    return () => clearTimeout(id);
  }, [searchInput, setSearchParams]);

  const filtered = useMemo(() => {
    if (!workflows) return [];
    if (!urlSearch.trim()) return workflows;
    const q = urlSearch.toLowerCase();
    return workflows.filter((wf) => wf.name.toLowerCase().includes(q));
  }, [workflows, urlSearch]);

  const handleRunNow = (workflowId: string) => {
    startRun.mutate({ workflowId });
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="font-display text-2xl font-bold text-ink">Workflows</h1>
          <DataFreshness dataUpdatedAt={dataUpdatedAt} onRefresh={refetch} isRefetching={isRefetching} />
        </div>
        <Button onClick={() => navigate("/workflows/new")}>
          <Plus className="h-4 w-4" />
          Create Workflow
        </Button>
      </div>

      {/* Active Runs Strip */}
      <ActiveRunsStrip />

      {/* Templates Section */}
      <section>
        <div className="mb-4 flex items-center gap-3">
          <h2 className="text-xs font-semibold uppercase tracking-wider text-muted">
            Templates
          </h2>
          <div className="relative max-w-xs flex-1">
            <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted" />
            <Input
              placeholder="Search workflows..."
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              className="pl-8 text-sm"
            />
          </div>
        </div>

        {isLoading && (
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
            <SkeletonCards />
          </div>
        )}

        {isError && (
          <Card>
            <p className="py-8 text-center text-muted">
              Failed to load workflows. Please try again.
            </p>
          </Card>
        )}

        {!isLoading && !isError && filtered.length > 0 && (
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
            {filtered.map((wf) => (
              <WorkflowTemplateCard
                key={wf.id}
                workflow={wf}
                onRunNow={handleRunNow}
              />
            ))}
          </div>
        )}

        {!isLoading && !isError && workflows && workflows.length > 0 && filtered.length === 0 && (
          <Card>
            <EmptyState
              icon={Workflow}
              title={`No workflows matching "${urlSearch}"`}
              description="Try a different search term."
            />
          </Card>
        )}

        {!isLoading && !isError && (!workflows || workflows.length === 0) && (
          <Card>
            <EmptyState
              icon={Workflow}
              title="No workflows yet"
              description="Create your first workflow to automate agent orchestration."
              action={
                <Button variant="outline" onClick={() => navigate("/workflows/new")}>
                  Create your first workflow
                </Button>
              }
            />
          </Card>
        )}
      </section>

      {/* Loading indicator for Run Now */}
      {startRun.isPending && (
        <div className="fixed bottom-4 right-4 flex items-center gap-2 rounded-xl bg-ink px-4 py-2.5 text-sm text-white shadow-lg">
          <Loader className="h-4 w-4 animate-spin" />
          Starting run...
        </div>
      )}
    </div>
  );
}
