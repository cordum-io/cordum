import { Suspense, useEffect, type ReactNode } from "react";
import { safeLazy as lazy } from "./lib/safeLazy";
import { BrowserRouter, Routes, Route, Navigate, useLocation, useParams, useSearchParams } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { NuqsAdapter } from "nuqs/adapters/react-router/v7";
import { MotionConfig } from "framer-motion";
import { Toaster } from "sonner";
import { registerQueryClient } from "./state/config";
import { useUiStore } from "./state/ui";
import { AppShell } from "./components/layout/AppShell";
import { LoadingScreen } from "./components/layout/LoadingScreen";
import { useRequireAuth } from "./hooks/useAuth";
import { useEventStream } from "./hooks/useEventStream";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { RouteBoundary } from "./components/RouteBoundary";
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
const ApprovalsPage = lazy(() => import("./pages/ApprovalsPage"));
const ApprovalDetailPage = lazy(() => import("./pages/approvals/ApprovalDetailPage"));
const WorkflowsPage = lazy(() => import("./pages/WorkflowsPage"));
const WorkflowStudioPage = lazy(() => import("./pages/WorkflowStudioPage"));
const RunDetailPage = lazy(() => import("./pages/RunDetailPage"));
const RunsPage = lazy(() => import("./pages/RunsPage"));
const PacksPage = lazy(() => import("./pages/PacksPage"));
const PackDetailPage = lazy(() => import("./pages/PackDetailPage"));
const SchemasPage = lazy(() => import("./pages/SchemasPage"));
const SchemaDetailPage = lazy(() => import("./pages/SchemaDetailPage"));
const TopicsPage = lazy(() => import("./pages/TopicsPage"));
const AuditLogPage = lazy(() => import("./pages/AuditLogPage"));
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
const SettingsShell = lazy(() => import("./pages/SettingsShell"));
const SettingsHubPage = lazy(() => import("./pages/SettingsHubPage"));

const GovernTenantDetailPage = lazy(() => import("./pages/govern/TenantDetailPage"));
const GovernBundleDetailPage = lazy(() => import("./pages/govern/BundleDetailPage"));
const QuarantinePage = lazy(() => import("./pages/govern/QuarantinePage"));
const SafetyControlsPage = lazy(() => import("./pages/govern/SafetyControlsPage"));
const GovernanceVerificationPage = lazy(() => import("./pages/govern/GovernanceVerificationPage"));
const EvalsPage = lazy(() => import("./pages/EvalsPage"));
const EvalDatasetDetailPage = lazy(() => import("./pages/EvalDatasetDetailPage"));
const EvalRunDetailPage = lazy(() => import("./pages/EvalRunDetailPage"));
const CopilotSessionPage = lazy(() => import("./pages/CopilotSessionPage"));
const EdgeSessionDetailPage = lazy(() => import("./pages/EdgeSessionDetailPage"));
const EdgeSessionsPage = lazy(() => import("./pages/EdgeSessionsPage"));

// Policy Studio v3 surfaces (epic-d9a6c0a1 Dashboard 1 foundation).
const PoliciesPage = lazy(() => import("./pages/policies/PoliciesPage"));
const PoliciesBundlesPage = lazy(() => import("./pages/policies/BundlesPage"));
const PoliciesBundleDetailPage = lazy(() => import("./pages/policies/BundleDetailPage"));
const PoliciesDecisionsPage = lazy(() => import("./pages/policies/DecisionsPage"));

// Policy Studio tab redirects — canonical `/govern/<tab>` aliases land on
// the tabbed overview with the right tab/mode pre-selected. These are not
// legacy redirects; they are the current public shortcuts operators use
// to deep-link into a specific Policy Studio tab.
// Backwards-compat redirect — the standalone /agents/identity/:id route was
// folded into the agent detail page as a tab during the v2.5 IA cut.
function AgentIdentityRedirect() {
  const { id } = useParams<{ id: string }>();
  if (!id) return <Navigate to="/agents" replace />;
  return <Navigate to={`/agents/${id}?tab=identity`} replace />;
}

export function DlqRouteRedirect() {
  return <Navigate to="/jobs?status=dlq" replace />;
}

/**
 * /govern/overview → /policies/* redirect (epic-d9a6c0a1 Dashboard 1).
 * Maps the legacy `?tab=` + `?mode=` query params onto the new three-surface IA.
 * Preserves any unrelated query params for bookmark compatibility.
 */
export function GovernOverviewRedirect() {
  const [searchParams] = useSearchParams();
  const tab = searchParams.get("tab") ?? "";
  const mode = searchParams.get("mode") ?? "";

  // Compute target path + querystring per the spec mapping.
  const params = new URLSearchParams(searchParams);
  params.delete("tab");
  params.delete("mode");

  let target = "/policies";
  switch (tab) {
    case "input-rules":
      params.set("type", "input");
      break;
    case "output-rules":
      params.set("type", "output");
      break;
    case "velocity":
    case "velocity-rules":
      params.set("type", "velocity");
      break;
    case "bundles":
      target = "/policies/bundles";
      break;
    case "scope":
      target = "/policies/bundles";
      params.set("view", "scope");
      break;
    case "evaluation":
      target = "/policies/decisions";
      if (mode) params.set("mode", mode);
      break;
    default:
      // Unknown / missing tab → land on /policies (Rules surface) per spec default.
      break;
  }

  const qs = params.toString();
  return <Navigate to={qs ? `${target}?${qs}` : target} replace />;
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
    <ErrorBoundaryWrapper>
    <ToastBridge />
    <AppShell>
      <Suspense fallback={<LoadingScreen />}>
        <Routes>
          {/* RUN / OPERATE */}
          <Route path="/" element={<RouteBoundary name="Overview"><HomePage /></RouteBoundary>} />
          <Route path="/agents" element={<RouteBoundary name="Agents"><AgentsPage /></RouteBoundary>} />
          <Route path="/agents/:id" element={<RouteBoundary name="Agent details"><AgentDetailPage /></RouteBoundary>} />
          <Route path="/agents/identity/:id" element={<AgentIdentityRedirect />} />
          <Route path="/jobs" element={<RouteBoundary name="Jobs"><JobsPage /></RouteBoundary>} />
          <Route path="/jobs/:id" element={<RouteBoundary name="Job details"><JobDetailPage /></RouteBoundary>} />
          <Route path="/edge/sessions" element={<RouteBoundary name="Edge sessions"><EdgeSessionsPage /></RouteBoundary>} />
          <Route path="/edge/sessions/:sessionId" element={<RouteBoundary name="Edge session details"><EdgeSessionDetailPage /></RouteBoundary>} />
          <Route path="/runs" element={<RouteBoundary name="Runs"><RunsPage /></RouteBoundary>} />

          {/* GOVERN */}
          <Route path="/approvals" element={<RouteBoundary name="Approvals"><ApprovalsPage /></RouteBoundary>} />
          <Route path="/approvals/:jobId" element={<RouteBoundary name="Approval details"><ApprovalDetailPage /></RouteBoundary>} />
          
          <Route path="/policies" element={<RouteBoundary name="Policy rules"><PoliciesPage /></RouteBoundary>} />
          <Route path="/policies/bundles" element={<RouteBoundary name="Policy bundles"><PoliciesBundlesPage /></RouteBoundary>} />
          <Route path="/policies/bundles/:id" element={<RouteBoundary name="Bundle details"><PoliciesBundleDetailPage /></RouteBoundary>} />
          <Route path="/policies/decisions" element={<RouteBoundary name="Policy decisions"><PoliciesDecisionsPage /></RouteBoundary>} />

          <Route path="/security/quarantine" element={<RouteBoundary name="Quarantine"><QuarantinePage /></RouteBoundary>} />
          <Route path="/security/safety" element={<RouteBoundary name="Safety Controls"><SafetyControlsPage /></RouteBoundary>} />
          <Route path="/govern/verification" element={<RouteBoundary name="Governance verification"><GovernanceVerificationPage /></RouteBoundary>} />
          <Route path="/govern/tenants/:id" element={<RouteBoundary name="Tenant details"><GovernTenantDetailPage /></RouteBoundary>} />
          <Route path="/govern/bundles/:id" element={<RouteBoundary name="Bundle details"><GovernBundleDetailPage /></RouteBoundary>} />
          <Route path="/govern/overview" element={<GovernOverviewRedirect />} />
          <Route path="/govern/quarantine" element={<Navigate to="/security/quarantine" replace />} />

          {/* BUILD / CATALOG */}
          <Route path="/workflows" element={<RouteBoundary name="Workflows"><WorkflowsPage /></RouteBoundary>} />
          <Route path="/workflows/studio/new" element={<RouteBoundary name="Workflow studio"><WorkflowStudioPage /></RouteBoundary>} />
          <Route path="/workflows/:id/studio" element={<RouteBoundary name="Workflow studio"><WorkflowStudioPage /></RouteBoundary>} />
          <Route path="/workflows/:id/runs/:runId" element={<RouteBoundary name="Run details"><RunDetailPage /></RouteBoundary>} />
          <Route path="/packs" element={<RouteBoundary name="Packs"><PacksPage /></RouteBoundary>} />
          <Route path="/packs/:id" element={<RouteBoundary name="Pack details"><PackDetailPage /></RouteBoundary>} />
          <Route path="/topics" element={<RouteBoundary name="Topics"><TopicsPage /></RouteBoundary>} />
          <Route path="/schemas" element={<RouteBoundary name="Schemas"><SchemasPage /></RouteBoundary>} />
          <Route path="/schemas/:id" element={<RouteBoundary name="Schema details"><SchemaDetailPage /></RouteBoundary>} />

          {/* AUDIT */}
          <Route path="/audit" element={<RouteBoundary name="Audit Trail"><AuditLogPage /></RouteBoundary>} />
          <Route path="/dlq" element={<DlqRouteRedirect />} />

          {/* OTHER */}
          {FEATURE_FLAGS.evalsPage && (
            <>
              <Route path="/evals" element={<RouteBoundary name="Evaluations"><EvalsPage /></RouteBoundary>} />
              <Route path="/evals/:datasetId" element={<RouteBoundary name="Eval dataset"><EvalDatasetDetailPage /></RouteBoundary>} />
              <Route path="/evals/runs/:runId" element={<RouteBoundary name="Eval run"><EvalRunDetailPage /></RouteBoundary>} />
            </>
          )}
          <Route path="/copilot/sessions/:sessionId" element={<RouteBoundary name="Copilot session"><CopilotSessionPage /></RouteBoundary>} />

          {/* SETTINGS */}
          <Route path="/settings" element={<RouteBoundary name="Settings"><SettingsShell /></RouteBoundary>}>
            <Route index element={<RouteBoundary name="Settings hub"><SettingsHubPage /></RouteBoundary>} />
            <Route path="health" element={<RouteBoundary name="Settings: health"><SettingsHealthPage /></RouteBoundary>} />
            <Route path="keys" element={<RouteBoundary name="Settings: API keys"><SettingsKeysPage /></RouteBoundary>} />
            <Route path="users" element={<RouteBoundary name="Settings: users"><SettingsUsersPage /></RouteBoundary>} />
            <Route path="notifications" element={<RouteBoundary name="Settings: notifications"><SettingsNotificationsPage /></RouteBoundary>} />
            <Route path="environments" element={<RouteBoundary name="Settings: environments"><SettingsEnvironmentsPage /></RouteBoundary>} />
            <Route path="config" element={<RouteBoundary name="Settings: config"><SettingsConfigPage /></RouteBoundary>} />
            <Route path="mcp" element={<RouteBoundary name="Settings: MCP"><SettingsMcpPage /></RouteBoundary>} />
            <Route path="sso" element={<RouteBoundary name="Settings: SSO"><SettingsSSOPage /></RouteBoundary>} />
            <Route path="scim" element={<RouteBoundary name="Settings: SCIM"><SettingsSCIMPage /></RouteBoundary>} />
            <Route path="audit-export" element={<RouteBoundary name="Settings: audit export"><SettingsAuditExportPage /></RouteBoundary>} />
            <Route path="license" element={<RouteBoundary name="Settings: license"><SettingsLicensePage /></RouteBoundary>} />
          </Route>

          <Route path="*" element={<RouteBoundary name="Page not found"><NotFoundPage /></RouteBoundary>} />
        </Routes>
      </Suspense>
    </AppShell>
    </ErrorBoundaryWrapper>
  );
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <MotionConfig reducedMotion="user">
        <BrowserRouter>
          <NuqsAdapter>
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
                <Route
                  path="/login"
                  element={
                    <RouteBoundary name="Login">
                      <LoginPage />
                    </RouteBoundary>
                  }
                />
                <Route path="/*" element={<ProtectedRoutes />} />
              </Routes>
            </Suspense>
          </NuqsAdapter>
        </BrowserRouter>
      </MotionConfig>
    </QueryClientProvider>
  );
}
