import { lazy, Suspense } from "react";
import { BrowserRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ProtectedRoute } from "./components/ProtectedRoute";
import { ErrorBoundary } from "./components/ErrorBoundary";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { refetchOnWindowFocus: false },
  },
});

// Lazy-loaded pages
const HomePage = lazy(() => import("./pages/HomePage"));
const LoginPage = lazy(() => import("./pages/LoginPage"));
const JobsPage = lazy(() => import("./pages/JobsPage"));
const JobDetailPage = lazy(() => import("./pages/JobDetailPage"));
const WorkflowsPage = lazy(() => import("./pages/WorkflowsPage"));
const WorkflowCreatePage = lazy(() => import("./pages/WorkflowCreatePage"));
const WorkflowDetailPage = lazy(() => import("./pages/WorkflowDetailPage"));
const AgentsPage = lazy(() => import("./pages/AgentsPage"));
const PolicyPage = lazy(() => import("./pages/PolicyPage"));
const ApprovalsPage = lazy(() => import("./pages/ApprovalsPage"));
const AuditLogPage = lazy(() => import("./pages/AuditLogPage"));
const DLQPage = lazy(() => import("./pages/DLQPage"));
const PacksPage = lazy(() => import("./pages/PacksPage"));
const SettingsPage = lazy(() => import("./pages/SettingsPage"));
const SchemasPage = lazy(() => import("./pages/SchemasPage"));
const SchemaDetailPage = lazy(() => import("./pages/SchemaDetailPage"));
const PoolsPage = lazy(() => import("./pages/PoolsPage"));
const SystemPage = lazy(() => import("./pages/SystemPage"));
const ContextPage = lazy(() => import("./pages/ContextPage"));
const TracePage = lazy(() => import("./pages/TracePage"));
const ToolsPage = lazy(() => import("./pages/ToolsPage"));
const SearchPage = lazy(() => import("./pages/SearchPage"));

function LoadingFallback() {
  return (
    <div className="flex min-h-[200px] items-center justify-center text-sm text-muted">
      Loading...
    </div>
  );
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <ErrorBoundary>
          <Suspense fallback={<LoadingFallback />}>
            <Routes>
              {/* Public route */}
              <Route path="/login" element={<LoginPage />} />

              {/* Protected routes inside ProtectedRoute (provides AppShell) */}
              <Route
                path="*"
                element={
                  <ProtectedRoute>
                    <ErrorBoundary>
                      <Suspense fallback={<LoadingFallback />}>
                        <Routes>
                      <Route path="/" element={<HomePage />} />
                      <Route path="/jobs" element={<JobsPage />} />
                      <Route path="/jobs/:id" element={<JobDetailPage />} />
                      <Route path="/workflows" element={<WorkflowsPage />} />
                      <Route path="/workflows/new" element={<WorkflowCreatePage />} />
                      <Route path="/workflows/:id" element={<WorkflowDetailPage />} />
                      <Route path="/workflows/:id/runs/:runId" element={<WorkflowDetailPage />} />
                      <Route path="/agents" element={<AgentsPage />} />
                      <Route path="/policy" element={<PolicyPage />} />
                      <Route path="/approvals" element={<ApprovalsPage />} />
                      <Route path="/audit" element={<AuditLogPage />} />
                      <Route path="/dlq" element={<DLQPage />} />
                      <Route path="/packs" element={<PacksPage />} />
                      <Route path="/settings" element={<SettingsPage />} />
                      <Route path="/schemas" element={<SchemasPage />} />
                      <Route path="/schemas/:id" element={<SchemaDetailPage />} />
                      <Route path="/pools" element={<PoolsPage />} />
                      <Route path="/system" element={<SystemPage />} />
                      <Route path="/context" element={<ContextPage />} />
                      <Route path="/trace" element={<TracePage />} />
                      <Route path="/tools" element={<ToolsPage />} />
                      <Route path="/search" element={<SearchPage />} />
                        </Routes>
                      </Suspense>
                    </ErrorBoundary>
                  </ProtectedRoute>
                }
              />
            </Routes>
          </Suspense>
        </ErrorBoundary>
      </BrowserRouter>
    </QueryClientProvider>
  );
}
