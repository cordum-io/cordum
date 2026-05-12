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
// DLQPage was deleted in task-100cc89c step 5 — /dlq folds into JobsPage
// as ?status=dlq via the DlqRouteRedirect below.
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
// Backwards-compat redirect — the standalone /agents/identity/:id route was
// folded into the agent detail page as a tab during the v2.5 IA cut.
function AgentIdentityRedirect() {
  const { id } = useParams<{ id: string }>();
  if (!id) return <Navigate to="/agents" replace />;
  return <Navigate to={`/agents/${encodeURIComponent(id)}?tab=identity`} replace />;
}

export function DlqRouteRedirect() {
  return <Navigate to="/jobs?status=dlq" replace />;
}

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
          {/* OPERATE */}
          <Route path="/" element={<RouteBoundary name="Overview"><HomePage /></RouteBoundary>} />
          <Route path="/agents" element={<RouteBoundary name="Agents"><AgentsPage /></RouteBoundary>} />
          <Route path="/agents/:id" element={<RouteBoundary name="Agent details"><AgentDetailPage /></RouteBoundary>} />
          <Route path="/agents/identity/:id" element={<AgentIdentityRedirect />} />
          {FEATURE_FLAGS.delegationDashboard && (
            <Route path="/delegations" element={<RouteBoundary name="Delegations"><DelegationsPage /></RouteBoundary>} />
          )}
          <Route path="/jobs" element={<RouteBoundary name="Jobs"><JobsPage /></RouteBoundary>} />
          <Route path="/jobs/:id" element={<RouteBoundary name="Job details"><JobDetailPage /></RouteBoundary>} />
          <Route path="/edge/sessions" element={<RouteBoundary name="Edge sessions"><EdgeSessionsPage /></RouteBoundary>} />
          <Route path="/edge/sessions/:sessionId" element={<RouteBoundary name="Edge session details"><EdgeSessionDetailPage /></RouteBoundary>} />

          {/* ORCHESTRATE */}
          <Route path="/workflows" element={<RouteBoundary name="Workflows"><WorkflowsPage /></RouteBoundary>} />
          <Route path="/workflows/studio/new" element={<RouteBoundary name="Workflow studio"><WorkflowStudioPage /></RouteBoundary>} />
          <Route path="/workflows/:id/studio" element={<RouteBoundary name="Workflow studio"><WorkflowStudioPage /></RouteBoundary>} />
          <Route path="/workflows/:id/runs/:runId" element={<RouteBoundary name="Run details"><RunDetailPage /></RouteBoundary>} />
          <Route path="/approvals" element={<RouteBoundary name="Approvals"><ApprovalsPage /></RouteBoundary>} />
          <Route path="/approvals/:jobId" element={<RouteBoundary name="Approval details"><ApprovalDetailPage /></RouteBoundary>} />

          {/* POLICY STUDIO (epic-d9a6c0a1 v3 IA) — three top-level surfaces */}

          {/* GOVERN — legacy redirects preserve bookmarks; canonical surfaces are /policies/* */}
          <Route path="/govern/overview" element={<RouteBoundary name="Policy overview"><GovernPolicyOverviewPage /></RouteBoundary>} />
          <Route path="/govern/input-rules" element={<PolicyTabRedirect tab="input-rules" />} />
          <Route path="/govern/output-rules" element={<PolicyTabRedirect tab="output-rules" />} />
          <Route path="/govern/velocity-rules" element={<PolicyTabRedirect tab="velocity" />} />
          <Route path="/govern/tenants" element={<PolicyTabRedirect tab="scope" />} />
          <Route path="/govern/tenants/:id" element={<RouteBoundary name="Tenant details"><GovernTenantDetailPage /></RouteBoundary>} />
          <Route path="/govern/bundles/:id" element={<RouteBoundary name="Bundle details"><GovernBundleDetailPage /></RouteBoundary>} />
          <Route path="/govern/bundles" element={<PolicyTabRedirect tab="bundles" />} />
          <Route path="/govern/simulator" element={<PolicyTabRedirect tab="evaluation" mode="simulator" />} />
          <Route path="/govern/quarantine" element={<RouteBoundary name="Quarantine"><GovernQuarantinePage /></RouteBoundary>} />
          <Route path="/govern/verification" element={<RouteBoundary name="Governance verification"><GovernanceVerificationPage /></RouteBoundary>} />
          <Route path="/govern/replay" element={<PolicyTabRedirect tab="evaluation" mode="replay" />} />
          <Route path="/govern/analytics" element={<PolicyTabRedirect tab="evaluation" mode="analytics" />} />
          {FEATURE_FLAGS.evalsPage && (
            <>
              <Route path="/evals" element={<RouteBoundary name="Evaluations"><EvalsPage /></RouteBoundary>} />
              <Route path="/evals/:datasetId" element={<RouteBoundary name="Eval dataset"><EvalDatasetDetailPage /></RouteBoundary>} />
              <Route path="/evals/runs/:runId" element={<RouteBoundary name="Eval run"><EvalRunDetailPage /></RouteBoundary>} />
            </>
          )}

          {/* COPILOT */}
          <Route path="/copilot/sessions/:sessionId" element={<RouteBoundary name="Copilot session"><CopilotSessionPage /></RouteBoundary>} />

          {/* EXTEND */}
          <Route path="/packs" element={<RouteBoundary name="Packs"><PacksPage /></RouteBoundary>} />
          <Route path="/packs/:id" element={<RouteBoundary name="Pack details"><PackDetailPage /></RouteBoundary>} />
          <Route path="/topics" element={<RouteBoundary name="Topics"><TopicsPage /></RouteBoundary>} />
          <Route path="/schemas" element={<RouteBoundary name="Schemas"><SchemasPage /></RouteBoundary>} />
          <Route path="/schemas/:id" element={<RouteBoundary name="Schema details"><SchemaDetailPage /></RouteBoundary>} />

          {/* OBSERVE */}
          <Route path="/audit" element={<RouteBoundary name="Audit log"><AuditLogPage /></RouteBoundary>} />
          {/* /dlq folds into JobsPage via ?status=dlq (task-0bcb9411 + page-
              deletion in task-100cc89c step 5). The redirect keeps existing
              bookmarks + sidebar entries working. */}
          <Route path="/dlq" element={<DlqRouteRedirect />} />

          {/* SETTINGS — single shell with grouped left sub-nav (v2.5 IA cut).
              Each leaf route gets its own RouteBoundary so a failure in one
              settings page leaves the rest of the shell + sidebar usable. */}
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
