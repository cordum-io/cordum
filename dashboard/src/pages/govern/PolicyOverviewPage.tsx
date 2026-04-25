import { useState, useMemo, useCallback, useRef, Suspense, lazy } from "react";
import { useSearchParams } from "react-router-dom";
import {
  Shield,
  FileInput,
  FileOutput,
  FlaskConical,
  Package,
  Zap,
  History,
  TrendingUp,
  Layers,
} from "lucide-react";
import { motion } from "framer-motion";
import { cn } from "@/lib/utils";
import { logger } from "@/lib/logger";
import { usePageTitle } from "@/hooks/usePageTitle";
import { PageHeader } from "@/components/layout/PageHeader";
import { Tabs } from "@/components/ui/Tabs";
import { InfoBanner } from "@/components/ui/InfoBanner";
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
  isValidEvaluationMode,
  type PolicyEvaluationMode,
} from "@/components/policy/tabs";
import type { PolicyBundle } from "@/api/types";

// --- Extracted types for local clarity ---
interface PolicyBundleActive extends PolicyBundle {
  active?: boolean;
}

// Lazy-loaded tab content — each page accepts { hideHeader?: boolean }
const LazyInputRulesTab = lazy(() => import("@/pages/govern/InputRulesPage")) as React.LazyExoticComponent<React.ComponentType<{ hideHeader?: boolean }>>;
const LazyOutputRulesTab = lazy(() => import("@/pages/govern/OutputRulesPage")) as React.LazyExoticComponent<React.ComponentType<{ hideHeader?: boolean }>>;
const LazySimulatorTab = lazy(() => import("@/pages/govern/SimulatorPage")) as React.LazyExoticComponent<React.ComponentType<{ hideHeader?: boolean }>>;
const LazyVelocityTab = lazy(() => import("@/pages/govern/VelocityRulesPage")) as React.LazyExoticComponent<React.ComponentType<{ hideHeader?: boolean }>>;
const LazyReplayTab = lazy(() => import("@/pages/govern/ReplayPage")) as React.LazyExoticComponent<React.ComponentType<{ hideHeader?: boolean }>>;
const LazyAnalyticsTab = lazy(() => import("@/pages/govern/PolicyAnalyticsPage")) as React.LazyExoticComponent<React.ComponentType<{ hideHeader?: boolean }>>;
const LazyBundlesTab = lazy(() => import("@/pages/govern/BundlesPage")) as React.LazyExoticComponent<React.ComponentType<{ hideHeader?: boolean }>>;
const LazyScopeTab = lazy(() => import("@/pages/govern/TenantsPage")) as React.LazyExoticComponent<React.ComponentType<{ hideHeader?: boolean }>>;

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
  { id: "velocity", label: "Velocity", icon: Zap },
  { id: "evaluation", label: "Evaluation", icon: TrendingUp },
  { id: "bundles", label: "Bundles", icon: Package },
  { id: "scope", label: "Scope", icon: Layers },
];

interface EvaluationModeDef {
  id: PolicyEvaluationMode;
  label: string;
  icon: typeof Shield;
  description: string;
}

const EVALUATION_MODES: EvaluationModeDef[] = [
  {
    id: "analytics",
    label: "Analytics",
    icon: TrendingUp,
    description:
      "See which rules generate the most volume, overrides, and approval fatigue before you tune them.",
  },
  {
    id: "replay",
    label: "Replay",
    icon: History,
    description:
      "Re-run historical traffic against the current or a candidate policy to see what decisions would change.",
  },
  {
    id: "simulator",
    label: "Simulator",
    icon: FlaskConical,
    description:
      "Test one request at a time when you need targeted what-if validation instead of a batch comparison.",
  },
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

  const bundles = (bundlesRes?.items ?? []) as PolicyBundleActive[];
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
    <motion.div
      className="space-y-6"
      initial="hidden"
      animate="visible"
      variants={{ visible: { transition: { staggerChildren: 0.05 } } }}
    >
      {/* Top Level Posture Summary — Full Width */}
      <motion.div variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}>
        <PostureSummary bundles={bundles} allRules={allRules} />
      </motion.div>

      {/* Explore Section */}
      <motion.div
        className="space-y-4 pt-4"
        variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}
      >
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-display font-semibold text-foreground">
            Explore Rule Base
          </h3>
        </div>
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

        <div className="instrument-card p-0 overflow-hidden bg-background/40 backdrop-blur-sm">
          <div className="flex items-center gap-1 border-b border-border/50 p-1">
            {viewTabs.map((t) => (
              <button
                key={t.id}
                type="button"
                onClick={() => setActiveView(t.id)}
                className={cn(
                  "px-4 py-1.5 text-xs font-medium rounded-xl transition-all",
                  activeView === t.id
                    ? "bg-surface-2 text-foreground shadow-sm"
                    : "text-muted-foreground hover:text-foreground hover:bg-surface-1/50",
                )}
              >
                {t.label}
                {typeof t.count === "number" && (
                  <span className="ml-1.5 opacity-50 font-mono text-[10px]">({t.count})</span>
                )}
              </button>
            ))}
          </div>

          <div className="p-5">
            {activeView === "bundles" && (
              <div className="space-y-4">
                {filteredBundles.length === 0 ? (
                  <div className="text-center py-12 text-muted-foreground">
                    <p className="text-sm">
                      {hasActiveFilter ? "No bundles match the current filters." : "No policy bundles installed."}
                    </p>
                  </div>
                ) : (
                  filteredBundles.map((bundle) => (
                    <BundleOverviewCard key={bundle.id} bundle={bundle} filterText={combinedFilter || undefined} />
                  ))
                )}
              </div>
            )}
            {activeView === "all-rules" && <AllRulesTable bundles={filteredBundles} filterText={combinedFilter || undefined} />}
            {activeView === "by-topic" && <ByTopicTable bundles={filteredBundles} filterText={combinedFilter || undefined} />}
          </div>
        </div>
      </motion.div>
    </motion.div>
  );
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

function StudioSectionIntro({
  eyebrow,
  title,
  description,
  children,
}: {
  eyebrow: string;
  title: string;
  description: string;
  children?: React.ReactNode;
}) {
  return (
    <section className="rounded-3xl border border-border bg-surface-1/70 p-5">
      <div className="space-y-2">
        <p className="text-xs font-mono uppercase tracking-[0.12em] text-muted-foreground">
          {eyebrow}
        </p>
        <div className="space-y-1">
          <h2 className="text-lg font-display font-semibold text-foreground">{title}</h2>
          <p className="max-w-3xl text-sm text-muted-foreground">{description}</p>
        </div>
      </div>
      {children && <div className="mt-4">{children}</div>}
    </section>
  );
}

// ---------------------------------------------------------------------------
// Policy Studio page
// ---------------------------------------------------------------------------

export default function PolicyOverviewPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const rawTab = searchParams.get("tab") ?? "overview";
  const normalizedTab = rawTab === "simulator" ? "evaluation" : rawTab;
  const activeTab: PolicyStudioTab = isValidTab(normalizedTab) ? normalizedTab : "overview";
  const rawMode = searchParams.get("mode") ?? (rawTab === "simulator" ? "simulator" : "analytics");
  const evaluationMode: PolicyEvaluationMode = isValidEvaluationMode(rawMode)
    ? rawMode
    : "analytics";

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
      const nextParams = new URLSearchParams(searchParams);
      nextParams.set("tab", newTab);
      if (newTab === "evaluation") {
        if (!isValidEvaluationMode(nextParams.get("mode") ?? "")) {
          nextParams.set("mode", "analytics");
        }
      } else {
        nextParams.delete("mode");
      }
      setSearchParams(nextParams, { replace: false });
    },
    [activeTab, searchParams, setSearchParams],
  );

  const handleEvaluationModeChange = useCallback(
    (mode: PolicyEvaluationMode) => {
      const nextParams = new URLSearchParams(searchParams);
      nextParams.set("tab", "evaluation");
      nextParams.set("mode", mode);
      setSearchParams(nextParams, { replace: false });
    },
    [searchParams, setSearchParams],
  );

  // Data for tab counts
  const { data: rulesRes } = usePolicyRules();
  const { data: bundlesRes } = usePolicyBundles();
  const inputRuleCount = rulesRes?.items?.length ?? 0;
  const bundleCount = bundlesRes?.items?.length ?? 0;

  const tabLabel = TABS.find((t) => t.id === activeTab)?.label ?? "Overview";
  const evaluationModeLabel =
    EVALUATION_MODES.find((mode) => mode.id === evaluationMode)?.label ?? "Analytics";
  usePageTitle(
    activeTab === "overview"
      ? "Policy Studio"
      : activeTab === "evaluation"
        ? `Policy Studio \u2014 Evaluation \u2014 ${evaluationModeLabel}`
        : `Policy Studio \u2014 ${tabLabel}`,
  );

  const primaryTabs = TABS.map((tab) => {
    const Icon = tab.icon;
    return {
      id: tab.id,
      label: tab.label,
      icon: <Icon className="h-4 w-4" />,
      count:
        tab.id === "input-rules"
          ? inputRuleCount
          : tab.id === "bundles"
            ? bundleCount
            : undefined,
    };
  });

  const evaluationTabs = EVALUATION_MODES.map((mode) => {
    const Icon = mode.icon;
    return {
      id: mode.id,
      label: mode.label,
      icon: <Icon className="h-4 w-4" />,
    };
  });

  const activeEvaluationMode =
    EVALUATION_MODES.find((mode) => mode.id === evaluationMode) ?? EVALUATION_MODES[0];

  return (
    <div className="space-y-6 animate-rise">
      <PageHeader
        label={`Govern \u00b7 Policy Studio`}
        title="Policy Studio"
        subtitle="Author rules, evaluate candidate changes, publish bundles, and roll out tenant scope from one governance workspace."
      />

      <Tabs
        tabs={primaryTabs}
        activeTab={activeTab}
        onChange={(id) => handleTabChange(id as PolicyStudioTab)}
        ariaLabel="Policy Studio sections"
        variant="segmented"
      />

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

      {activeTab === "velocity" && (
        <div className="space-y-5">
          <StudioSectionIntro
            eyebrow="Author"
            title="Velocity controls"
            description="Keep sliding-window escalations beside the rest of policy authoring so rate-based protections are tuned in the same workflow as input and output rules."
          />
          <Suspense fallback={<TabSkeleton />}>
            <LazyVelocityTab hideHeader />
          </Suspense>
        </div>
      )}

      {activeTab === "evaluation" && (
        <div className="space-y-5">
          <StudioSectionIntro
            eyebrow="Evaluate"
            title="Policy evaluation"
            description="Use one evaluation workspace for live rule quality, historical replay, and targeted simulation so you can understand what is happening, compare what would change, and then tune or publish with confidence."
          >
            <Tabs
              tabs={evaluationTabs}
              activeTab={evaluationMode}
              onChange={(id) => handleEvaluationModeChange(id as PolicyEvaluationMode)}
              ariaLabel="Policy evaluation modes"
              variant="segmented"
              className="w-fit"
            />
          </StudioSectionIntro>

          <InfoBanner variant="cordum" title={activeEvaluationMode.label}>
            {activeEvaluationMode.description}
          </InfoBanner>

          {evaluationMode === "analytics" && (
            <Suspense fallback={<TabSkeleton />}>
              <LazyAnalyticsTab hideHeader />
            </Suspense>
          )}

          {evaluationMode === "replay" && (
            <Suspense fallback={<TabSkeleton />}>
              <LazyReplayTab hideHeader />
            </Suspense>
          )}

          {evaluationMode === "simulator" && (
            <Suspense fallback={<TabSkeleton />}>
              <LazySimulatorTab hideHeader />
            </Suspense>
          )}
        </div>
      )}

      {activeTab === "bundles" && (
        <Suspense fallback={<TabSkeleton />}>
          <LazyBundlesTab hideHeader />
        </Suspense>
      )}

      {activeTab === "scope" && (
        <div className="space-y-5">
          <StudioSectionIntro
            eyebrow="Roll out"
            title="Scope and rollout"
            description="Inspect tenant boundaries, bundle-backed rollout state, and MCP governance posture without leaving Policy Studio."
          />
          <Suspense fallback={<TabSkeleton />}>
            <LazyScopeTab hideHeader />
          </Suspense>
        </div>
      )}
    </div>
  );
}
