import { Suspense, useEffect, type ReactNode } from "react";
import { safeLazy as lazy } from "./lib/safeLazy";
import { BrowserRouter, Routes, Route, Navigate, useLocation, useSearchParams } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MotionConfig } from "framer-motion";
import { Toaster } from "sonner";
import { registerQueryClient } from "./state/config";
import { useUiStore } from "./state/ui";
import { AppShell } from "./components/layout/AppShell";
import { LoadingScreen } from "./components/layout/LoadingScreen";
import { useRequireAuth } from "./hooks/useAuth";
import { useEventStream } from "./hooks/useEventStream";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { ToastBridge } from "./components/ToastBridge";
import { FEATURE_FLAGS } from "./config/flags";

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      retry: 2,
      refetchOnWindowFocus: true,
    },
  },
});

// Register QueryClient so config store can clear cache on logout/tenant-switch
registerQueryClient(queryClient);

// Lazy-loaded pages
const LoginPage = lazy(() => import("./pages/LoginPage"));
const HomePage = lazy(() => import("./pages/HomePage"));
const JobsPage = lazy(() => import("./pages/JobsPage"));
const JobDetailPage = lazy(() => import("./pages/JobDetailPage"));
const AgentsPage = lazy(() => import("./pages/AgentsPage"));
const AgentDetailPage = lazy(() => import("./pages/AgentDetailPage"));
const AgentIdentityDetailPage = lazy(() => import("./pages/AgentIdentityDetailPage"));
const DelegationsPage = lazy(() => import("./pages/DelegationsPage"));
const ApprovalsPage = lazy(() => import("./pages/ApprovalsPage"));
const ApprovalDetailPage = lazy(() => import("./pages/approvals/ApprovalDetailPage"));
const WorkflowsPage = lazy(() => import("./pages/WorkflowsPage"));
const WorkflowStudioPage = lazy(() => import("./pages/WorkflowStudioPage"));
const RunDetailPage = lazy(() => import("./pages/RunDetailPage"));
const PacksPage = lazy(() => import("./pages/PacksPage"));
const PackDetailPage = lazy(() => import("./pages/PackDetailPage"));
const SchemasPage = lazy(() => import("./pages/SchemasPage"));
const SchemaDetailPage = lazy(() => import("./pages/SchemaDetailPage"));
const TopicsPage = lazy(() => import("./pages/TopicsPage"));
const AuditLogPage = lazy(() => import("./pages/AuditLogPage"));
const DLQPage = lazy(() => import("./pages/DLQPage"));
const SettingsHealthPage = lazy(() => import("./pages/SettingsHealthPage"));
const SettingsKeysPage = lazy(() => import("./pages/SettingsKeysPage"));
const SettingsUsersPage = lazy(() => import("./pages/SettingsUsersPage"));
const SettingsNotificationsPage = lazy(() => import("./pages/SettingsNotificationsPage"));
const SettingsEnvironmentsPage = lazy(() => import("./pages/SettingsEnvironmentsPage"));
const SettingsConfigPage = lazy(() => import("./pages/SettingsConfigPage"));
const SettingsMcpPage = lazy(() => import("./pages/SettingsMcpPage"));
const SettingsLicensePage = lazy(() => import("./pages/settings/LicensePage"));
const SettingsSSOPage = lazy(() => import("./pages/settings/SettingsSSOPage"));
const SettingsSCIMPage = lazy(() => import("./pages/settings/SettingsSCIMPage"));
const SettingsAuditExportPage = lazy(() => import("./pages/settings/SettingsAuditExportPage"));
const NotFoundPage = lazy(() => import("./pages/NotFoundPage"));
const SettingsHubPage = lazy(() => import("./pages/SettingsHubPage"));
const GovernPolicyOverviewPage = lazy(() => import("./pages/govern/PolicyOverviewPage"));
const GovernTenantDetailPage = lazy(() => import("./pages/govern/TenantDetailPage"));
const GovernBundleDetailPage = lazy(() => import("./pages/govern/BundleDetailPage"));
const GovernQuarantinePage = lazy(() => import("./pages/govern/QuarantinePage"));
const GovernanceVerificationPage = lazy(() => import("./pages/govern/GovernanceVerificationPage"));
const EvalsPage = lazy(() => import("./pages/EvalsPage"));
const EvalDatasetDetailPage = lazy(() => import("./pages/EvalDatasetDetailPage"));
const EvalRunDetailPage = lazy(() => import("./pages/EvalRunDetailPage"));
const CopilotSessionPage = lazy(() => import("./pages/CopilotSessionPage"));
const EdgeSessionDetailPage = lazy(() => import("./pages/EdgeSessionDetailPage"));
const EdgeSessionsPage = lazy(() => import("./pages/EdgeSessionsPage"));

// Policy Studio tab redirects — canonical `/govern/<tab>` aliases land on
// the tabbed overview with the right tab/mode pre-selected. These are not
// legacy redirects; they are the current public shortcuts operators use
// to deep-link into a specific Policy Studio tab.
function PolicyTabRedirect({ tab, mode }: { tab: string; mode?: string }) {
  const [searchParams] = useSearchParams();
  const params = new URLSearchParams(searchParams);
  params.set("tab", tab);
  if (mode) {
    params.set("mode", mode);
  } else {
    params.delete("mode");
  }
  return <Navigate to={`/govern/overview?${params.toString()}`} replace />;
}

function ThemeSync() {
  const resolvedTheme = useUiStore((s) => s.resolvedTheme);

  useEffect(() => {
    const root = document.documentElement;
    root.classList.remove("light", "dark");
    root.classList.add(resolvedTheme);
    root.style.colorScheme = resolvedTheme;
  }, [resolvedTheme]);

  return null;
}

function ErrorBoundaryWrapper({ children }: { children: ReactNode }) {
  const location = useLocation();
  return <ErrorBoundary resetKey={location.pathname}>{children}</ErrorBoundary>;
}

function ProtectedRoutes() {
  const isAuthenticated = useRequireAuth();
  useEventStream();
  if (!isAuthenticated) return null;

  return (
    <>
    <ToastBridge />
    <AppShell>
      <ErrorBoundaryWrapper>
      <Suspense fallback={<LoadingScreen />}>
        <Routes>
          {/* OPERATE */}
          <Route path="/" element={<HomePage />} />
          <Route path="/agents" element={<AgentsPage />} />
          <Route path="/agents/:id" element={<AgentDetailPage />} />
          <Route path="/agents/identity/:id" element={<AgentIdentityDetailPage />} />
          {FEATURE_FLAGS.delegationDashboard && (
            <Route path="/delegations" element={<DelegationsPage />} />
          )}
          <Route path="/jobs" element={<JobsPage />} />
          <Route path="/jobs/:id" element={<JobDetailPage />} />
          <Route path="/edge/sessions" element={<EdgeSessionsPage />} />
          <Route path="/edge/sessions/:sessionId" element={<EdgeSessionDetailPage />} />

          {/* ORCHESTRATE */}
          <Route path="/workflows" element={<WorkflowsPage />} />
          <Route path="/workflows/studio/new" element={<WorkflowStudioPage />} />
          <Route path="/workflows/:id/studio" element={<WorkflowStudioPage />} />
          <Route path="/workflows/:id/runs/:runId" element={<RunDetailPage />} />
          <Route path="/approvals" element={<ApprovalsPage />} />
          <Route path="/approvals/:jobId" element={<ApprovalDetailPage />} />

          {/* GOVERN */}
          <Route path="/govern/overview" element={<GovernPolicyOverviewPage />} />
          <Route path="/govern/input-rules" element={<PolicyTabRedirect tab="input-rules" />} />
          <Route path="/govern/output-rules" element={<PolicyTabRedirect tab="output-rules" />} />
          <Route path="/govern/velocity-rules" element={<PolicyTabRedirect tab="velocity" />} />
          <Route path="/govern/tenants" element={<PolicyTabRedirect tab="scope" />} />
          <Route path="/govern/tenants/:id" element={<GovernTenantDetailPage />} />
          <Route path="/govern/bundles/:id" element={<GovernBundleDetailPage />} />
          <Route path="/govern/bundles" element={<PolicyTabRedirect tab="bundles" />} />
          <Route path="/govern/simulator" element={<PolicyTabRedirect tab="evaluation" mode="simulator" />} />
          <Route path="/govern/quarantine" element={<GovernQuarantinePage />} />
          <Route path="/govern/verification" element={<GovernanceVerificationPage />} />
          <Route path="/govern/replay" element={<PolicyTabRedirect tab="evaluation" mode="replay" />} />
          <Route path="/govern/analytics" element={<PolicyTabRedirect tab="evaluation" mode="analytics" />} />
          {FEATURE_FLAGS.evalsPage && (
            <>
              <Route path="/evals" element={<EvalsPage />} />
              <Route path="/evals/:datasetId" element={<EvalDatasetDetailPage />} />
              <Route path="/evals/runs/:runId" element={<EvalRunDetailPage />} />
            </>
          )}

          {/* COPILOT */}
          <Route path="/copilot/sessions/:sessionId" element={<CopilotSessionPage />} />

          {/* EXTEND */}
          <Route path="/packs" element={<PacksPage />} />
          <Route path="/packs/:id" element={<PackDetailPage />} />
          <Route path="/topics" element={<TopicsPage />} />
          <Route path="/schemas" element={<SchemasPage />} />
          <Route path="/schemas/:id" element={<SchemaDetailPage />} />

          {/* OBSERVE */}
          <Route path="/audit" element={<AuditLogPage />} />
          <Route path="/dlq" element={<DLQPage />} />

          {/* SETTINGS */}
          <Route path="/settings" element={<SettingsHubPage />} />
          <Route path="/settings/health" element={<SettingsHealthPage />} />
          <Route path="/settings/keys" element={<SettingsKeysPage />} />
          <Route path="/settings/users" element={<SettingsUsersPage />} />
          <Route path="/settings/notifications" element={<SettingsNotificationsPage />} />
          <Route path="/settings/environments" element={<SettingsEnvironmentsPage />} />
          <Route path="/settings/config" element={<SettingsConfigPage />} />
          <Route path="/settings/mcp" element={<SettingsMcpPage />} />
          <Route path="/settings/sso" element={<SettingsSSOPage />} />
          <Route path="/settings/scim" element={<SettingsSCIMPage />} />
          <Route path="/settings/audit-export" element={<SettingsAuditExportPage />} />
          <Route path="/settings/license" element={<SettingsLicensePage />} />

          <Route path="*" element={<NotFoundPage />} />
        </Routes>
      </Suspense>
      </ErrorBoundaryWrapper>
    </AppShell>
    </>
  );
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <MotionConfig reducedMotion="user">
        <BrowserRouter>
          <ThemeSync />
          <Toaster
            position="top-right"
            toastOptions={{
              style: {
                background: "var(--surface)",
                color: "var(--text)",
                border: "1px solid var(--border-color)",
                fontFamily: "var(--font-sans)",
              },
            }}
          />
          <Suspense fallback={<LoadingScreen />}>
            <Routes>
              <Route path="/login" element={<LoginPage />} />
              <Route path="/*" element={<ProtectedRoutes />} />
            </Routes>
          </Suspense>
        </BrowserRouter>
      </MotionConfig>
    </QueryClientProvider>
  );
}
