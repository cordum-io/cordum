import { Suspense, lazy, useEffect, useMemo, type ReactNode } from "react";
import { Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { AppShell } from "../components/layout/AppShell";
import { CommandPalette } from "../components/CommandPalette";
import { useLiveBus } from "../hooks/useLiveBus";
import { useUiStore } from "../state/ui";
import { useAuthConfig } from "../hooks/useAuthConfig";
import { useConfigStore } from "../state/config";

const HomePage = lazy(() => import("../pages/HomePage").then((m) => ({ default: m.HomePage })));
const RunsPage = lazy(() => import("../pages/RunsPage").then((m) => ({ default: m.RunsPage })));
const JobsPage = lazy(() => import("../pages/JobsPage").then((m) => ({ default: m.JobsPage })));
const JobDetailPage = lazy(() => import("../pages/JobDetailPage").then((m) => ({ default: m.JobDetailPage })));
const RunDetailPage = lazy(() => import("../pages/RunDetailPage").then((m) => ({ default: m.RunDetailPage })));
const WorkflowsPage = lazy(() => import("../pages/WorkflowsPage").then((m) => ({ default: m.WorkflowsPage })));
const WorkflowCreatePage = lazy(() => import("../pages/WorkflowCreatePage").then((m) => ({ default: m.WorkflowCreatePage })));
const WorkflowDetailPage = lazy(() => import("../pages/WorkflowDetailPage").then((m) => ({ default: m.WorkflowDetailPage })));
const PacksPage = lazy(() => import("../pages/PacksPage").then((m) => ({ default: m.PacksPage })));
const PoolsPage = lazy(() => import("../pages/PoolsPage").then((m) => ({ default: m.PoolsPage })));
const PolicyPage = lazy(() => import("../pages/PolicyPage").then((m) => ({ default: m.PolicyPage })));
const SystemPage = lazy(() => import("../pages/SystemPage").then((m) => ({ default: m.SystemPage })));
const ToolsPage = lazy(() => import("../pages/ToolsPage").then((m) => ({ default: m.ToolsPage })));
const TracePage = lazy(() => import("../pages/TracePage").then((m) => ({ default: m.TracePage })));
const SearchPage = lazy(() => import("../pages/SearchPage").then((m) => ({ default: m.SearchPage })));
const NotFoundPage = lazy(() => import("../pages/NotFoundPage").then((m) => ({ default: m.NotFoundPage })));
const LoginPage = lazy(() => import("../pages/LoginPage").then((m) => ({ default: m.LoginPage })));
const AuthCallbackPage = lazy(() => import("../pages/AuthCallbackPage").then((m) => ({ default: m.AuthCallbackPage })));

function AuthGate({ children }: { children: ReactNode }) {
  const location = useLocation();
  const apiKey = useConfigStore((state) => state.apiKey);
  const loaded = useConfigStore((state) => state.loaded);
  const { data: authConfig, isLoading } = useAuthConfig();

  if (!loaded || isLoading) {
    return <div className="min-h-screen bg-[color:var(--surface-muted)] p-8 text-sm text-muted">Loading console...</div>;
  }
  const requiresAuth = !!authConfig && (authConfig.password_enabled || authConfig.saml_enabled);
  if (requiresAuth && !apiKey) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  }
  return <>{children}</>;
}

function MainApp() {
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
      { id: "pools", title: "Go to Pools & Workers", group: "Navigation", onSelect: () => navigate("/pools") },
      { id: "policy", title: "Go to Policy", group: "Navigation", onSelect: () => navigate("/policy") },
      { id: "system", title: "Go to System", group: "Navigation", onSelect: () => navigate("/system") },
      { id: "tools", title: "Go to Tools", group: "Navigation", onSelect: () => navigate("/tools") },
      { id: "trace", title: "Trace Explorer", group: "Navigation", onSelect: () => navigate("/trace") },
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
        <Routes>
          <Route path="/" element={<HomePage />} />
          <Route path="/search" element={<SearchPage />} />
          <Route path="/runs" element={<RunsPage />} />
          <Route path="/runs/:runId" element={<RunDetailPage />} />
          <Route path="/jobs" element={<JobsPage />} />
          <Route path="/jobs/:jobId" element={<JobDetailPage />} />
          <Route path="/workflows" element={<WorkflowsPage />} />
          <Route path="/workflows/new" element={<WorkflowCreatePage />} />
          <Route path="/workflows/:workflowId" element={<WorkflowDetailPage />} />
          <Route path="/packs" element={<PacksPage />} />
          <Route path="/pools" element={<PoolsPage />} />
          <Route path="/policy" element={<PolicyPage />} />
          <Route path="/system" element={<SystemPage />} />
          <Route path="/tools" element={<ToolsPage />} />
          <Route path="/trace" element={<TracePage />} />
          <Route path="/trace/:id" element={<TracePage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Routes>
      </AppShell>
      <CommandPalette items={actions} />
    </>
  );
}

export function App() {
  return (
    <Suspense fallback={<div className="text-sm text-muted">Loading dashboard...</div>}>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/auth/callback" element={<AuthCallbackPage />} />
        <Route
          path="/*"
          element={
            <AuthGate>
              <MainApp />
            </AuthGate>
          }
        />
      </Routes>
    </Suspense>
  );
}
