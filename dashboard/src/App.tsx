import { Suspense, useEffect, type ReactNode } from "react";
import { safeLazy as lazy } from "./lib/safeLazy";
import { BrowserRouter, Routes, Route, Navigate, useLocation, useParams, useSearchParams } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import { registerQueryClient } from "./state/config";
import { useUiStore } from "./state/ui";
import { AppShell } from "./components/layout/AppShell";
import { LoadingScreen } from "./components/layout/LoadingScreen";
import { useRequireAuth } from "./hooks/useAuth";
import { useEventStream } from "./hooks/useEventStream";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { ToastBridge } from "./components/ToastBridge";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      retry: 2,
      refetchOnWindowFocus: true,
      refetchIntervalInBackground: true,
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
const GovernVelocityRulesPage = lazy(() => import("./pages/govern/VelocityRulesPage"));
const GovernTenantsPage = lazy(() => import("./pages/govern/TenantsPage"));
const GovernTenantDetailPage = lazy(() => import("./pages/govern/TenantDetailPage"));
const GovernBundleDetailPage = lazy(() => import("./pages/govern/BundleDetailPage"));
const GovernQuarantinePage = lazy(() => import("./pages/govern/QuarantinePage"));
const GovernReplayPage = lazy(() => import("./pages/govern/ReplayPage"));
const GovernPolicyAnalyticsPage = lazy(() => import("./pages/govern/PolicyAnalyticsPage"));

// Policy Studio tab redirects — old standalone page routes → tabbed Policy Studio
function PolicyTabRedirect({ tab }: { tab: string }) {
  const [searchParams] = useSearchParams();
  const params = new URLSearchParams(searchParams);
  params.set("tab", tab);
  return <Navigate to={`/govern/overview?${params.toString()}`} replace />;
}

// Legacy workflow redirects (bookmarks / external links)
function WorkflowViewRedirect() {
  const { id } = useParams<{ id: string }>();
  return <Navigate to={`/workflows/${id}/studio`} replace />;
}
function WorkflowEditRedirect() {
  const { id } = useParams<{ id: string }>();
  return <Navigate to={`/workflows/${id}/studio?mode=edit`} replace />;
}

export const LEGACY_POLICY_ROUTE_REDIRECTS = {
  root: "/govern/overview",
  builder: "/govern/overview",
  rules: "/govern/overview",
  input: "/govern/overview?tab=input-rules",
  output: "/govern/overview?tab=output-rules",
  tenants: "/govern/tenants",
  bundles: "/govern/overview?tab=bundles",
  simulator: "/govern/overview?tab=simulator",
  velocityRules: "/govern/velocity-rules",
  history: "/govern/overview?tab=bundles",
  analytics: "/govern/overview?tab=simulator",
  publish: "/govern/overview?tab=bundles",
  quarantine: "/govern/quarantine",
} as const;

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
          <Route path="/jobs" element={<JobsPage />} />
          <Route path="/jobs/:id" element={<JobDetailPage />} />

          {/* ORCHESTRATE */}
          <Route path="/workflows" element={<WorkflowsPage />} />
          <Route path="/workflows/studio/new" element={<WorkflowStudioPage />} />
          <Route path="/workflows/:id/studio" element={<WorkflowStudioPage />} />
          <Route path="/workflows/:id/runs/:runId" element={<RunDetailPage />} />
          {/* Legacy workflow redirects */}
          <Route path="/workflows/new" element={<Navigate to="/workflows/studio/new" replace />} />
          <Route path="/workflows/:id/edit" element={<WorkflowEditRedirect />} />
          <Route path="/workflows/:id" element={<WorkflowViewRedirect />} />
          <Route path="/approvals" element={<ApprovalsPage />} />
          <Route path="/approvals/:jobId" element={<ApprovalDetailPage />} />

          {/* GOVERN */}
          <Route path="/govern/overview" element={<GovernPolicyOverviewPage />} />
          <Route path="/govern/input-rules" element={<PolicyTabRedirect tab="input-rules" />} />
          <Route path="/govern/output-rules" element={<PolicyTabRedirect tab="output-rules" />} />
          <Route path="/govern/velocity-rules" element={<GovernVelocityRulesPage />} />
          <Route path="/govern/tenants" element={<GovernTenantsPage />} />
          <Route path="/govern/tenants/:id" element={<GovernTenantDetailPage />} />
          <Route path="/govern/bundles/:id" element={<GovernBundleDetailPage />} />
          <Route path="/govern/bundles" element={<PolicyTabRedirect tab="bundles" />} />
          <Route path="/govern/simulator" element={<PolicyTabRedirect tab="simulator" />} />
          <Route path="/govern/quarantine" element={<GovernQuarantinePage />} />
          <Route path="/govern/replay" element={<GovernReplayPage />} />
          <Route path="/govern/analytics" element={<GovernPolicyAnalyticsPage />} />
          <Route path="/policies" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.root} replace />} />
          <Route path="/policies/input" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.input} replace />} />
          <Route path="/policies/rules" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.rules} replace />} />
          <Route path="/policies/rules/new" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.rules} replace />} />
          <Route path="/policies/rules/:id" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.rules} replace />} />
          <Route
            path="/policies/builder"
            element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.builder} replace />}
          />
          <Route path="/policies/output" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.output} replace />} />
          <Route path="/policies/hierarchy" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.tenants} replace />} />
          <Route path="/policies/bundles" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.bundles} replace />} />
          <Route path="/policies/simulator" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.simulator} replace />} />
          <Route path="/policies/history" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.history} replace />} />
          <Route path="/policies/analytics" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.analytics} replace />} />
          <Route path="/policies/publish" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.publish} replace />} />
          <Route path="/policies/*" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.root} replace />} />
          <Route path="/quarantine" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.quarantine} replace />} />

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

          {/* Legacy redirects */}
          <Route path="/pools" element={<Navigate to="/agents" replace />} />
          <Route path="/system" element={<Navigate to="/settings/health" replace />} />
          <Route path="/security" element={<Navigate to="/" replace />} />
          <Route path="/traces" element={<Navigate to="/jobs" replace />} />
          <Route path="/safety/input" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.input} replace />} />
          <Route path="/safety/output" element={<Navigate to={LEGACY_POLICY_ROUTE_REDIRECTS.output} replace />} />
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
    </QueryClientProvider>
  );
}
