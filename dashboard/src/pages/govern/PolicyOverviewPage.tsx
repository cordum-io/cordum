import { useState, useMemo, useCallback, useRef, Suspense, lazy } from "react";
import { useSearchParams } from "react-router-dom";
import {
  Shield,
  FileInput,
  FileOutput,
  FlaskConical,
  Package,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { logger } from "@/lib/logger";
import { usePageTitle } from "@/hooks/usePageTitle";
import { PageHeader } from "@/components/layout/PageHeader";
import { SkeletonCard } from "@/components/ui/Skeleton";
import {
  PostureSummary,
  PolicyFilterBar,
  BundleOverviewCard,
  AllRulesTable,
  ByTopicTable,
  type PolicyScope,
} from "@/components/policy/overview";
import { usePolicyBundles, usePolicyRules } from "@/hooks/usePolicies";
import { Loader2 } from "lucide-react";
import {
  isValidTab,
  type PolicyStudioTab,
} from "@/components/policy/tabs";
import type { PolicyBundle } from "@/api/types";
import { ChainIntegrityWidget } from "@/components/ChainIntegrityWidget";
import { GapAlertBanner } from "@/components/GapAlertBanner";
import { useAuth } from "@/hooks/useAuth";

// Lazy-loaded tab content — each page accepts { hideHeader?: boolean }
const LazyInputRulesTab = lazy(() => import("@/pages/govern/InputRulesPage")) as React.LazyExoticComponent<React.ComponentType<{ hideHeader?: boolean }>>;
const LazyOutputRulesTab = lazy(() => import("@/pages/govern/OutputRulesPage")) as React.LazyExoticComponent<React.ComponentType<{ hideHeader?: boolean }>>;
const LazySimulatorTab = lazy(() => import("@/pages/govern/SimulatorPage")) as React.LazyExoticComponent<React.ComponentType<{ hideHeader?: boolean }>>;
const LazyBundlesTab = lazy(() => import("@/pages/govern/BundlesPage")) as React.LazyExoticComponent<React.ComponentType<{ hideHeader?: boolean }>>;

// ---------------------------------------------------------------------------
// Tab definitions
// ---------------------------------------------------------------------------

interface TabDef {
  id: PolicyStudioTab;
  label: string;
  icon: typeof Shield;
}

const TABS: TabDef[] = [
  { id: "overview", label: "Overview", icon: Shield },
  { id: "input-rules", label: "Input Rules", icon: FileInput },
  { id: "output-rules", label: "Output Rules", icon: FileOutput },
  { id: "simulator", label: "Simulator", icon: FlaskConical },
  { id: "bundles", label: "Bundles", icon: Package },
];

// ---------------------------------------------------------------------------
// Overview helpers (kept from original)
// ---------------------------------------------------------------------------

function matchesScope(bundle: PolicyBundle, scope: PolicyScope): boolean {
  if (scope === "all") return true;
  const rules = bundle.rules ?? [];
  if (scope === "global") {
    return rules.some((r) => !r.match?.tenants || r.match.tenants.length === 0);
  }
  if (scope === "tenant") {
    return rules.some((r) => r.match?.tenants && r.match.tenants.length > 0);
  }
  if (scope === "workflow") {
    return rules.some(
      (r) =>
        r.match?.topics?.some((t) => t.includes("workflow")) ||
        r.match?.capabilities?.some((c) => c.includes("workflow")),
    );
  }
  return true;
}

function matchesFilter(
  bundle: PolicyBundle,
  searchText: string,
  tenantFilter: string,
  topicFilter: string,
  capabilityFilter: string,
): boolean {
  const rules = bundle.rules ?? [];
  const lower = searchText.toLowerCase();
  const tenantLower = tenantFilter.toLowerCase();
  const topicLower = topicFilter.toLowerCase();
  const capLower = capabilityFilter.toLowerCase();
  const bundleMatch = !lower || bundle.name.toLowerCase().includes(lower) || bundle.id.toLowerCase().includes(lower);
  const ruleMatch = !lower || rules.some((r) => r.name.toLowerCase().includes(lower) || r.id.toLowerCase().includes(lower) || r.decision?.toLowerCase().includes(lower) || r.reason?.toLowerCase().includes(lower));
  const tenantMatch = !tenantLower || rules.some((r) => r.match?.tenants?.some((t) => t.toLowerCase().includes(tenantLower)));
  const topicMatch = !topicLower || rules.some((r) => r.match?.topics?.some((t) => t.toLowerCase().includes(topicLower)));
  const capMatch = !capLower || rules.some((r) => r.match?.capabilities?.some((c) => c.toLowerCase().includes(capLower)));
  return (bundleMatch || ruleMatch) && tenantMatch && topicMatch && capMatch;
}

function countScopeRules(bundles: PolicyBundle[], scope: PolicyScope): number {
  if (scope === "all") {
    return bundles.reduce((sum, b) => sum + (b.rules?.length ?? 0), 0);
  }
  return bundles
    .filter((b) => matchesScope(b, scope))
    .reduce((sum, b) => sum + (b.rules?.length ?? 0), 0);
}

// ---------------------------------------------------------------------------
// Overview tab content (extracted from old page)
// ---------------------------------------------------------------------------

function OverviewTabContent() {
  const { data: bundlesRes, isLoading: bundlesLoading } = usePolicyBundles();
  const { data: rulesRes, isLoading: rulesLoading } = usePolicyRules();

  const [searchText, setSearchText] = useState("");
  const [tenantFilter, setTenantFilter] = useState("");
  const [topicFilter, setTopicFilter] = useState("");
  const [capabilityFilter, setCapabilityFilter] = useState("");
  const [scope, setScope] = useState<PolicyScope>("all");
  const [activeView, setActiveView] = useState<"bundles" | "all-rules" | "by-topic">("bundles");

  const bundles = bundlesRes?.items ?? [];
  const allRules = rulesRes?.items ?? [];
  const hasActiveFilter = searchText !== "" || tenantFilter !== "" || topicFilter !== "" || capabilityFilter !== "" || scope !== "all";
  const clearFilters = useCallback(() => {
    setSearchText("");
    setTenantFilter("");
    setTopicFilter("");
    setCapabilityFilter("");
    setScope("all");
  }, []);

  const filteredBundles = useMemo(
    () => bundles.filter((b) => matchesScope(b, scope)).filter((b) => matchesFilter(b, searchText, tenantFilter, topicFilter, capabilityFilter)),
    [bundles, scope, searchText, tenantFilter, topicFilter, capabilityFilter],
  );

  const scopeCounts = useMemo(
    () => ({
      all: bundles.reduce((sum, b) => sum + (b.rules?.length ?? 0), 0),
      global: countScopeRules(bundles, "global"),
      tenant: countScopeRules(bundles, "tenant"),
      workflow: countScopeRules(bundles, "workflow"),
    }),
    [bundles],
  );

  const combinedFilter = [searchText, tenantFilter, topicFilter, capabilityFilter].filter(Boolean).join(" ");
  const isLoading = bundlesLoading || rulesLoading;

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-20">
        <Loader2 className="w-6 h-6 text-cordum animate-spin" />
        <span className="ml-3 text-sm text-muted-foreground">Loading policy data...</span>
      </div>
    );
  }

  const viewTabs = [
    { id: "bundles" as const, label: "Bundles", count: filteredBundles.length },
    { id: "all-rules" as const, label: "All Rules", count: filteredBundles.reduce((s, b) => s + (b.rules?.length ?? 0), 0) },
    { id: "by-topic" as const, label: "By Topic", count: filteredBundles.reduce((s, b) => s + (b.rules?.length ?? 0), 0) },
  ];

  return (
    <div className="space-y-6">
      <PostureSummary bundles={bundles} allRules={allRules} />
      <ChainIntegrityWidgetForOverview />
      <PolicyFilterBar
        searchText={searchText}
        onSearchChange={setSearchText}
        tenantFilter={tenantFilter}
        onTenantFilterChange={setTenantFilter}
        topicFilter={topicFilter}
        onTopicFilterChange={setTopicFilter}
        capabilityFilter={capabilityFilter}
        onCapabilityFilterChange={setCapabilityFilter}
        scope={scope}
        onScopeChange={setScope}
        scopeCounts={scopeCounts}
        onClear={clearFilters}
        hasActiveFilter={hasActiveFilter}
      />

      {/* Sub-view tabs */}
      <div className="flex items-center gap-1 border-b border-border pb-px">
        {viewTabs.map((t) => (
          <button
            key={t.id}
            type="button"
            onClick={() => setActiveView(t.id)}
            className={cn(
              "px-3 py-1.5 text-xs font-medium rounded-t-lg transition-colors",
              activeView === t.id
                ? "bg-surface-1 text-foreground border-b-2 border-[var(--primary)]"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {t.label}
            {typeof t.count === "number" && (
              <span className="ml-1.5 text-[10px] text-muted-foreground">({t.count})</span>
            )}
          </button>
        ))}
      </div>

      {activeView === "bundles" && (
        <div className="space-y-4">
          {filteredBundles.length === 0 ? (
            <div className="text-center py-12 text-muted-foreground">
              <p className="text-sm">
                {hasActiveFilter ? "No bundles match the current filters." : "No policy bundles installed."}
              </p>
            </div>
          ) : (
            <>
              <span className="text-xs font-mono text-muted-foreground uppercase tracking-widest block">
                Installed Bundles ({filteredBundles.length})
              </span>
              {filteredBundles.map((bundle) => (
                <BundleOverviewCard key={bundle.id} bundle={bundle} filterText={combinedFilter || undefined} />
              ))}
            </>
          )}
        </div>
      )}
      {activeView === "all-rules" && <AllRulesTable bundles={filteredBundles} filterText={combinedFilter || undefined} />}
      {activeView === "by-topic" && <ByTopicTable bundles={filteredBundles} filterText={combinedFilter || undefined} />}
    </div>
  );
}

// ChainIntegrityWidgetForOverview keeps the overview tab a function
// component while still letting the auth hook live in the right scope.
function ChainIntegrityWidgetForOverview() {
  const { tenantId } = useAuth();
  return <ChainIntegrityWidget tenant={tenantId ?? ""} />;
}

// GapAlertBannerForPage mounts at the top of PolicyOverviewPage to call
// out compromised-chain status before the user drills into bundles.
function GapAlertBannerForPage() {
  const { tenantId } = useAuth();
  return <GapAlertBanner tenant={tenantId ?? ""} />;
}

// ---------------------------------------------------------------------------
// Tab fallback
// ---------------------------------------------------------------------------

function TabSkeleton() {
  return (
    <div className="space-y-4 pt-2">
      <SkeletonCard />
      <SkeletonCard />
      <SkeletonCard />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Policy Studio page
// ---------------------------------------------------------------------------

export default function PolicyOverviewPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const rawTab = searchParams.get("tab") ?? "overview";
  const activeTab: PolicyStudioTab = isValidTab(rawTab) ? rawTab : "overview";

  // Dirty-state guard for tab switching (output rules can have unsaved edits)
  const tabDirtyRef = useRef<Record<string, () => boolean>>({});

  const handleTabChange = useCallback(
    (newTab: PolicyStudioTab) => {
      const currentDirtyCheck = tabDirtyRef.current[activeTab];
      if (currentDirtyCheck?.()) {
        const confirmed = window.confirm("You have unsaved changes. Switch tabs anyway?");
        if (!confirmed) {
          logger.warn("policy-studio", "Tab switch blocked by dirty state", { from: activeTab, to: newTab });
          return;
        }
      }
      setSearchParams({ tab: newTab }, { replace: false });
    },
    [activeTab, setSearchParams],
  );

  // Data for tab counts
  const { data: rulesRes } = usePolicyRules();
  const { data: bundlesRes } = usePolicyBundles();
  const inputRuleCount = rulesRes?.items?.length ?? 0;
  const bundleCount = bundlesRes?.items?.length ?? 0;

  const tabLabel = TABS.find((t) => t.id === activeTab)?.label ?? "Overview";
  usePageTitle(activeTab === "overview" ? "Policy Studio" : `Policy Studio \u2014 ${tabLabel}`);

  return (
    <div className="space-y-6 animate-rise">
      {/* Compromised-chain alert — session-dismissible, mounts above
          the page header so compliance reviewers see it first. */}
      <GapAlertBannerForPage />

      {/* Page Header */}
      <PageHeader
        label={`Govern \u00b7 Policy Studio`}
        title="Policy Studio"
        subtitle="Unified policy management \u2014 rules, output enforcement, simulation, and bundle lifecycle."
      />

      {/* Tab Bar */}
      <div className="flex items-center gap-0.5 bg-surface-1 border border-border rounded-2xl p-1 overflow-x-auto">
        {TABS.map((tab) => {
          const Icon = tab.icon;
          const isActive = activeTab === tab.id;
          const count =
            tab.id === "input-rules" ? inputRuleCount :
            tab.id === "bundles" ? bundleCount :
            undefined;

          return (
            <button
              key={tab.id}
              type="button"
              onClick={() => handleTabChange(tab.id)}
              className={cn(
                "flex items-center gap-2 px-4 py-2 rounded-xl text-sm font-medium transition-all duration-150 whitespace-nowrap",
                isActive
                  ? "bg-card text-foreground shadow-soft"
                  : "text-muted-foreground hover:text-foreground hover:bg-surface-2",
              )}
            >
              <Icon className="w-4 h-4" />
              {tab.label}
              {typeof count === "number" && count > 0 && (
                <span className={cn(
                  "text-[10px] font-mono px-1.5 py-0.5 rounded-full",
                  isActive ? "bg-[var(--primary)]/10 text-[var(--primary)]" : "bg-muted text-muted-foreground",
                )}>
                  {count}
                </span>
              )}
            </button>
          );
        })}
      </div>

      {/* Tab Content */}
      {activeTab === "overview" && <OverviewTabContent />}

      {activeTab === "input-rules" && (
        <Suspense fallback={<TabSkeleton />}>
          <LazyInputRulesTab hideHeader />
        </Suspense>
      )}

      {activeTab === "output-rules" && (
        <Suspense fallback={<TabSkeleton />}>
          <LazyOutputRulesTab hideHeader />
        </Suspense>
      )}

      {activeTab === "simulator" && (
        <Suspense fallback={<TabSkeleton />}>
          <LazySimulatorTab hideHeader />
        </Suspense>
      )}

      {activeTab === "bundles" && (
        <Suspense fallback={<TabSkeleton />}>
          <LazyBundlesTab hideHeader />
        </Suspense>
      )}
    </div>
  );
}
