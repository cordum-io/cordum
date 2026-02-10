import { lazy, Suspense } from "react";
import { BrowserRouter, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { QueryClient, QueryClientProvider, QueryCache, MutationCache } from "@tanstack/react-query";
import { ProtectedRoute } from "./components/ProtectedRoute";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { LoadingScreen } from "./components/ui/Spinner";
import { logger } from "./lib/logger";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { refetchOnWindowFocus: false },
  },
  queryCache: new QueryCache({
    onError: (error, query) => {
      logger.error("react-query", "Query failed", {
        queryKey: query.queryKey,
        error: error.message,
      });
    },
  }),
  mutationCache: new MutationCache({
    onError: (error) => {
      logger.error("react-query", "Mutation failed", {
        error: error.message,
      });
    },
  }),
});

// Lazy-loaded pages
const HomePage = lazy(() => import("./pages/HomePage"));
const LoginPage = lazy(() => import("./pages/LoginPage"));
const JobsPage = lazy(() => import("./pages/JobsPage"));
const JobDetailPage = lazy(() => import("./pages/JobDetailPage"));
const WorkflowsPage = lazy(() => import("./pages/WorkflowsPage"));
const WorkflowCreatePage = lazy(() => import("./pages/WorkflowCreatePage"));
const WorkflowDetailPage = lazy(() => import("./pages/WorkflowDetailPage"));
const RunDetailPage = lazy(() => import("./pages/RunDetailPage"));
const AgentsPage = lazy(() => import("./pages/AgentsPage"));
const PolicyLayout = lazy(() => import("./components/policy/PolicyLayout"));
const PoliciesOverviewPage = lazy(() => import("./pages/PoliciesOverviewPage"));
const PoliciesRulesPage = lazy(() => import("./pages/PoliciesRulesPage"));
const PoliciesBuilderPage = lazy(() => import("./pages/PoliciesBuilderPage"));
const PoliciesSimulatorPage = lazy(() => import("./pages/PoliciesSimulatorPage"));
const PoliciesHistoryPage = lazy(() => import("./pages/PoliciesHistoryPage"));
const PoliciesAnalyticsPage = lazy(() => import("./pages/PoliciesAnalyticsPage"));
const ApprovalsPage = lazy(() => import("./pages/ApprovalsPage"));
const AuditLogPage = lazy(() => import("./pages/AuditLogPage"));
const DLQPage = lazy(() => import("./pages/DLQPage"));
const PacksPage = lazy(() => import("./pages/PacksPage"));
const SettingsLayout = lazy(() => import("./components/settings/SettingsLayout"));
const SettingsHealthPage = lazy(() => import("./pages/SettingsHealthPage"));
const SettingsKeysPage = lazy(() => import("./pages/SettingsKeysPage"));
const SettingsUsersPage = lazy(() => import("./pages/SettingsUsersPage"));
const SettingsNotificationsPage = lazy(() => import("./pages/SettingsNotificationsPage"));
const SettingsEnvironmentsPage = lazy(() => import("./pages/SettingsEnvironmentsPage"));
const SettingsConfigPage = lazy(() => import("./pages/SettingsConfigPage"));
const SchemasPage = lazy(() => import("./pages/SchemasPage"));
const SchemaDetailPage = lazy(() => import("./pages/SchemaDetailPage"));
const NotFoundPage = lazy(() => import("./pages/NotFoundPage"));


function LocationAwareErrorBoundary({ children }: { children: React.ReactNode }) {
  const location = useLocation();
  return <ErrorBoundary resetKey={location.pathname}>{children}</ErrorBoundary>;
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <ErrorBoundary>
          <Suspense fallback={<LoadingScreen />}>
            <Routes>
              {/* Public route */}
              <Route path="/login" element={<LoginPage />} />

              {/* Protected routes inside ProtectedRoute (provides AppShell) */}
              <Route
                path="*"
                element={
                  <ProtectedRoute>
                    <LocationAwareErrorBoundary>
                      <Suspense fallback={<LoadingScreen />}>
                        <Routes>
                      <Route path="/" element={<HomePage />} />
                      <Route path="/jobs" element={<JobsPage />} />
                      <Route path="/jobs/:id" element={<JobDetailPage />} />
                      <Route path="/workflows" element={<WorkflowsPage />} />
                      <Route path="/workflows/new" element={<WorkflowCreatePage />} />
                      <Route path="/workflows/:id/edit" element={<WorkflowCreatePage />} />
                      <Route path="/workflows/:id" element={<WorkflowDetailPage />} />
                      <Route path="/workflows/:id/runs/:runId" element={<RunDetailPage />} />
                      <Route path="/agents" element={<AgentsPage />} />
                      <Route path="/policies" element={<PolicyLayout />}>
                        <Route index element={<PoliciesOverviewPage />} />
                        <Route path="rules" element={<PoliciesRulesPage />} />
                        <Route path="rules/new" element={<PoliciesBuilderPage />} />
                        <Route path="rules/:id" element={<PoliciesBuilderPage />} />
                        <Route path="simulator" element={<PoliciesSimulatorPage />} />
                        <Route path="history" element={<PoliciesHistoryPage />} />
                        <Route path="analytics" element={<PoliciesAnalyticsPage />} />
                      </Route>
                      <Route path="/policy" element={<Navigate to="/policies" replace />} />
                      <Route path="/approvals" element={<ApprovalsPage />} />
                      <Route path="/audit" element={<AuditLogPage />} />
                      <Route path="/dlq" element={<DLQPage />} />
                      <Route path="/packs" element={<PacksPage />} />
                      <Route path="/settings" element={<SettingsLayout />}>
                        <Route index element={<Navigate to="health" replace />} />
                        <Route path="health" element={<SettingsHealthPage />} />
                        <Route path="keys" element={<SettingsKeysPage />} />
                        <Route path="users" element={<SettingsUsersPage />} />
                        <Route path="notifications" element={<SettingsNotificationsPage />} />
                        <Route path="environments" element={<SettingsEnvironmentsPage />} />
                        <Route path="config" element={<SettingsConfigPage />} />
                      </Route>
                      <Route path="/schemas" element={<SchemasPage />} />
                      <Route path="/schemas/:id" element={<SchemaDetailPage />} />
                      {/* Legacy redirects */}
                      <Route path="/pools" element={<Navigate to="/agents" replace />} />
                      <Route path="/system" element={<Navigate to="/settings/health" replace />} />
                      <Route path="*" element={<NotFoundPage />} />
                        </Routes>
                      </Suspense>
                    </LocationAwareErrorBoundary>
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
