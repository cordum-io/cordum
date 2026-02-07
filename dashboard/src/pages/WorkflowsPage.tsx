import { useState, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { Plus, Search, Loader } from "lucide-react";
import { useWorkflows, useStartRun } from "../hooks/useWorkflows";
import { ActiveRunsStrip } from "../components/workflows/ActiveRunsStrip";
import { WorkflowTemplateCard } from "../components/workflows/WorkflowTemplateCard";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { Input } from "../components/ui/Input";

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
  const navigate = useNavigate();
  const { data: workflows, isLoading, isError } = useWorkflows();
  const startRun = useStartRun();
  const [search, setSearch] = useState("");

  const filtered = useMemo(() => {
    if (!workflows) return [];
    if (!search.trim()) return workflows;
    const q = search.toLowerCase();
    return workflows.filter((wf) => wf.name.toLowerCase().includes(q));
  }, [workflows, search]);

  const handleRunNow = (workflowId: string) => {
    startRun.mutate({ workflowId });
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="font-display text-2xl font-bold text-ink">Workflows</h1>
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
              value={search}
              onChange={(e) => setSearch(e.target.value)}
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
            <p className="py-8 text-center text-sm text-muted">
              No workflows matching &ldquo;{search}&rdquo;
            </p>
          </Card>
        )}

        {!isLoading && !isError && (!workflows || workflows.length === 0) && (
          <Card>
            <div className="py-12 text-center">
              <p className="text-muted">No workflows yet.</p>
              <Button
                variant="outline"
                className="mt-4"
                onClick={() => navigate("/workflows/new")}
              >
                Create your first workflow
              </Button>
            </div>
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
