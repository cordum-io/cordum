import { Suspense, lazy, useEffect, useMemo } from "react";
import { Route, Routes, useNavigate } from "react-router-dom";
import { AppShell } from "../components/layout/AppShell";
import { CommandPalette } from "../components/CommandPalette";
import { useLiveBus } from "../hooks/useLiveBus";
import { useUiStore } from "../state/ui";

const HomePage = lazy(() => import("../pages/HomePage").then((m) => ({ default: m.HomePage })));
const RunsPage = lazy(() => import("../pages/RunsPage").then((m) => ({ default: m.RunsPage })));
const JobsPage = lazy(() => import("../pages/JobsPage").then((m) => ({ default: m.JobsPage })));
const JobDetailPage = lazy(() => import("../pages/JobDetailPage").then((m) => ({ default: m.JobDetailPage })));
const RunDetailPage = lazy(() => import("../pages/RunDetailPage").then((m) => ({ default: m.RunDetailPage })));
const WorkflowsPage = lazy(() => import("../pages/WorkflowsPage").then((m) => ({ default: m.WorkflowsPage })));
const WorkflowDetailPage = lazy(() => import("../pages/WorkflowDetailPage").then((m) => ({ default: m.WorkflowDetailPage })));
const PacksPage = lazy(() => import("../pages/PacksPage").then((m) => ({ default: m.PacksPage })));
const PolicyPage = lazy(() => import("../pages/PolicyPage").then((m) => ({ default: m.PolicyPage })));
const SystemPage = lazy(() => import("../pages/SystemPage").then((m) => ({ default: m.SystemPage })));
const SearchPage = lazy(() => import("../pages/SearchPage").then((m) => ({ default: m.SearchPage })));
const NotFoundPage = lazy(() => import("../pages/NotFoundPage").then((m) => ({ default: m.NotFoundPage })));

export function App() {
  useLiveBus();
  const navigate = useNavigate();
  const setCommandOpen = useUiStore((state) => state.setCommandOpen);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setCommandOpen(true);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [setCommandOpen]);

  const actions = useMemo(
    () => [
      { id: "home", title: "Go to Home", group: "Navigation", onSelect: () => navigate("/") },
      { id: "runs", title: "Go to Runs", group: "Navigation", onSelect: () => navigate("/runs") },
      { id: "jobs", title: "Go to Jobs", group: "Navigation", onSelect: () => navigate("/jobs") },
      { id: "workflows", title: "Go to Workflows", group: "Navigation", onSelect: () => navigate("/workflows") },
      { id: "packs", title: "Go to Packs", group: "Navigation", onSelect: () => navigate("/packs") },
      { id: "policy", title: "Go to Policy", group: "Navigation", onSelect: () => navigate("/policy") },
      { id: "system", title: "Go to System", group: "Navigation", onSelect: () => navigate("/system") },
      { id: "search", title: "Open Search", group: "Navigation", onSelect: () => navigate("/search") },
      {
        id: "start-run",
        title: "Start new run",
        description: "Pick a workflow and launch a run",
        group: "Actions",
        onSelect: () => navigate("/workflows"),
      },
      {
        id: "approvals",
        title: "Review pending approvals",
        description: "Open the approvals inbox",
        group: "Actions",
        onSelect: () => navigate("/policy"),
      },
      {
        id: "dlq",
        title: "Review DLQ entries",
        description: "Investigate failed jobs",
        group: "Actions",
        onSelect: () => navigate("/system"),
      },
    ],
    [navigate]
  );

  return (
    <>
      <AppShell>
        <Suspense fallback={<div className="text-sm text-muted">Loading dashboard...</div>}>
          <Routes>
            <Route path="/" element={<HomePage />} />
            <Route path="/search" element={<SearchPage />} />
            <Route path="/runs" element={<RunsPage />} />
            <Route path="/runs/:runId" element={<RunDetailPage />} />
            <Route path="/jobs" element={<JobsPage />} />
            <Route path="/jobs/:jobId" element={<JobDetailPage />} />
            <Route path="/workflows" element={<WorkflowsPage />} />
            <Route path="/workflows/:workflowId" element={<WorkflowDetailPage />} />
            <Route path="/packs" element={<PacksPage />} />
            <Route path="/policy" element={<PolicyPage />} />
            <Route path="/system" element={<SystemPage />} />
            <Route path="*" element={<NotFoundPage />} />
          </Routes>
        </Suspense>
      </AppShell>
      <CommandPalette items={actions} />
    </>
  );
}
