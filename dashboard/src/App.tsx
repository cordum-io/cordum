import { Suspense, lazy, useEffect, type ReactNode } from "react";
import { BrowserRouter, Routes, Route, Navigate, useLocation } from "react-router-dom";
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
      refetchOnWindowFocus: false,
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
const WorkflowsPage = lazy(() => import("./pages/WorkflowsPage"));
const WorkflowDetailPage = lazy(() => import("./pages/WorkflowDetailPage"));
const WorkflowCreatePage = lazy(() => import("./pages/WorkflowCreatePage"));
const RunDetailPage = lazy(() => import("./pages/RunDetailPage"));
const PacksPage = lazy(() => import("./pages/PacksPage"));
const SchemasPage = lazy(() => import("./pages/SchemasPage"));
const SchemaDetailPage = lazy(() => import("./pages/SchemaDetailPage"));
const AuditLogPage = lazy(() => import("./pages/AuditLogPage"));
const DLQPage = lazy(() => import("./pages/DLQPage"));
const SettingsHealthPage = lazy(() => import("./pages/SettingsHealthPage"));
const SettingsKeysPage = lazy(() => import("./pages/SettingsKeysPage"));
const SettingsUsersPage = lazy(() => import("./pages/SettingsUsersPage"));
const SettingsNotificationsPage = lazy(() => import("./pages/SettingsNotificationsPage"));
const SettingsEnvironmentsPage = lazy(() => import("./pages/SettingsEnvironmentsPage"));
const SettingsConfigPage = lazy(() => import("./pages/SettingsConfigPage"));
const SettingsMcpPage = lazy(() => import("./pages/SettingsMcpPage"));
const NotFoundPage = lazy(() => import("./pages/NotFoundPage"));
const SettingsHubPage = lazy(() => import("./pages/SettingsHubPage"));
const GovernInputRulesPage = lazy(() => import("./pages/govern/InputRulesPage"));
const GovernOutputRulesPage = lazy(() => import("./pages/govern/OutputRulesPage"));
const GovernTenantsPage = lazy(() => import("./pages/govern/TenantsPage"));
const GovernTenantDetailPage = lazy(() => import("./pages/govern/TenantDetailPage"));
const GovernBundlesPage = lazy(() => import("./pages/govern/BundlesPage"));
const GovernBundleDetailPage = lazy(() => import("./pages/govern/BundleDetailPage"));
const GovernSimulatorPage = lazy(() => import("./pages/govern/SimulatorPage"));
const GovernQuarantinePage = lazy(() => import("./pages/govern/QuarantinePage"));

export const LEGACY_POLICY_ROUTE_REDIRECTS = {
  root: "/govern/input-rules",
  builder: "/govern/input-rules",
  rules: "/govern/input-rules",
  input: "/govern/input-rules",
  output: "/govern/output-rules",
  tenants: "/govern/tenants",
  bundles: "/govern/bundles",
  simulator: "/govern/simulator",
  history: "/govern/bundles",
  analytics: "/govern/simulator",
  publish: "/govern/bundles",
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
          <Route path="/workflows/new" element={<WorkflowCreatePage />} />
          <Route path="/workflows/:id/edit" element={<WorkflowCreatePage />} />
          <Route path="/workflows/:id" element={<WorkflowDetailPage />} />
          <Route path="/workflows/:id/runs/:runId" element={<RunDetailPage />} />
          <Route path="/approvals" element={<ApprovalsPage />} />

          {/* GOVERN */}
          <Route path="/govern/input-rules" element={<GovernInputRulesPage />} />
          <Route path="/govern/output-rules" element={<GovernOutputRulesPage />} />
          <Route path="/govern/tenants" element={<GovernTenantsPage />} />
          <Route path="/govern/tenants/:id" element={<GovernTenantDetailPage />} />
          <Route path="/govern/bundles" element={<GovernBundlesPage />} />
          <Route path="/govern/bundles/:id" element={<GovernBundleDetailPage />} />
          <Route path="/govern/simulator" element={<GovernSimulatorPage />} />
          <Route path="/govern/quarantine" element={<GovernQuarantinePage />} />
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
