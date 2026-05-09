import { Suspense, lazy, useMemo } from "react";
import { Link, useParams } from "react-router-dom";
import { useQueryState, parseAsStringLiteral } from "nuqs";
import { ArrowLeft, GitBranch } from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { Tabs } from "@/components/ui/Tabs";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import { useBundle, useBundleVersions, useBundleDeployments } from "@/hooks/useBundle";

const BundleRulesTab = lazy(() => import("./BundleRulesTab"));
const BundleVersionsTab = lazy(() => import("./BundleVersionsTab"));
const BundleDeploymentsTab = lazy(() => import("./BundleDeploymentsTab"));
const BundleDiffTab = lazy(() => import("./BundleDiffTab"));

const TAB_IDS = ["rules", "versions", "deployments", "diff"] as const;
type TabId = (typeof TAB_IDS)[number];

/**
 * Policy Studio — Bundle detail surface (Dashboard 5 step 4b).
 *
 * Renders a single Bundle's PageHeader + 4-tab navigation. Each tab is
 * code-split via `lazy()` so the detail-route chunk stays small; only the
 * active tab's bundle is fetched after the user navigates to it.
 *
 * Active tab is mirrored to the URL via nuqs (`?tab=rules|versions|
 * deployments|diff`) so deep-links + back/forward work. Default tab is
 * `rules`.
 */
export default function BundleDetailPage() {
  const { id } = useParams<{ id: string }>();
  const bundleId = id ?? "";

  const [tab, setTab] = useQueryState<TabId>(
    "tab",
    parseAsStringLiteral(TAB_IDS).withDefault("rules"),
  );

  const { data: bundle, isPending, isError } = useBundle(bundleId);
  const { data: versionsData } = useBundleVersions(bundleId);
  const { data: deploymentsData } = useBundleDeployments(bundleId);

  const tabs = useMemo(
    () => [
      {
        id: "rules" as const,
        label: "Rules",
        count: bundle?.rule_ids?.length,
      },
      {
        id: "versions" as const,
        label: "Versions",
        count: versionsData?.items.length,
      },
      {
        id: "deployments" as const,
        label: "Deployments",
        count: deploymentsData?.items.length,
      },
      {
        id: "diff" as const,
        label: "Diff",
      },
    ],
    [bundle?.rule_ids, versionsData?.items.length, deploymentsData?.items.length],
  );

  const scopeLabel = useMemo(() => {
    const scope = bundle?.scope_binding;
    if (!scope) return "—";
    if (scope.kind === "global") return "global";
    return scope.value ? `${scope.kind}:${scope.value}` : scope.kind;
  }, [bundle?.scope_binding]);

  if (isError) {
    return (
      <div className="space-y-6">
        <PageHeader label="Policy Studio" title="Bundle not found" />
        <EmptyState
          icon={<GitBranch className="h-5 w-5" />}
          title="Couldn't load this bundle"
          description="The bundle id may be invalid or the backend is unavailable."
          action={
            <Link
              to="/policies/bundles"
              className="inline-flex items-center gap-1 text-cordum hover:underline text-sm"
            >
              <ArrowLeft className="h-3.5 w-3.5" aria-hidden /> Back to bundles
            </Link>
          }
        />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <Link
        to="/policies/bundles"
        className="inline-flex items-center gap-1 text-xs font-mono text-muted-foreground hover:text-cordum transition-colors uppercase tracking-widest"
      >
        <ArrowLeft className="h-3.5 w-3.5" aria-hidden />
        Policy Bundles
      </Link>
      <PageHeader
        label="Policy Studio"
        title={isPending ? bundleId : bundle?.name ?? bundleId}
        subtitle={`Scope: ${scopeLabel}`}
        actions={
          <StatusBadge
            variant={bundle?.versions?.length ? "healthy" : "muted"}
            dot
          >
            {bundle?.versions?.length ? "Deployed" : "Draft"}
          </StatusBadge>
        }
      />

      <Tabs
        tabs={tabs}
        activeTab={tab}
        onChange={(next) => {
          // The string-literal parser narrows next to TabId already, but
          // setTab's signature wants TabId | null so we coerce explicitly.
          void setTab(next as TabId);
        }}
        ariaLabel="Bundle detail tabs"
        variant="underline"
      />

      <div role="tabpanel" aria-label={`${tab} tab content`}>
        <Suspense fallback={<TabLoadingFallback />}>
          {tab === "rules" && <BundleRulesTab bundleId={bundleId} />}
          {tab === "versions" && <BundleVersionsTab bundleId={bundleId} />}
          {tab === "deployments" && <BundleDeploymentsTab bundleId={bundleId} />}
          {tab === "diff" && <BundleDiffTab bundleId={bundleId} />}
        </Suspense>
      </div>
    </div>
  );
}

function TabLoadingFallback() {
  return (
    <div className="text-sm text-muted-foreground py-6 text-center">Loading…</div>
  );
}
