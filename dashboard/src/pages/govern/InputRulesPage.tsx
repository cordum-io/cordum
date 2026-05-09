import { useMemo, useState, useCallback, useRef, useEffect } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { motion, AnimatePresence } from "framer-motion";
import {
  ChevronDown,
  ChevronUp,
  FlaskConical,
  Package,
  Pencil,
  Search,
  Target,
  X,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { buildSimulatorUrl } from "@/lib/policy-studio/simulatorQuery";
import { InputRuleEditorDrawer } from "@/components/policy/input-rules/InputRuleEditorDrawer";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { Input } from "@/components/ui/Input";
import { Select } from "@/components/ui/Select";
import { getDecisionDisplayVariant } from "@/components/policy/simulator/SimulatorDecisionSummary";
import { usePolicyRules, usePolicyBundles } from "@/hooks/usePolicies";
import { usePolicyAccess } from "@/hooks/usePolicyAccess";
import { useWorkflows } from "@/hooks/useWorkflows";
import type { PolicyRule } from "@/api/types";
import type { GlobalPolicyInputDecision, GlobalPolicyInputRule } from "@/types/policy";

// ---------------------------------------------------------------------------
// Types & constants
// ---------------------------------------------------------------------------

export type InputRulesPageViewMode = "visual" | "split" | "yaml";
type ScopeFilter = "all" | "global" | "tenant" | "workflow";
type RuleScope = "global" | "tenant" | "workflow";

export const INPUT_RULES_PAGE_SECTIONS = [
  "first-match-banner",
  "default-decision",
  "ordered-rules",
  "yaml-pane",
] as const;

interface EnrichedRule {
  rule: PolicyRule;
  scope: RuleScope;
  scopeLabel: string;
  bundleName: string;
  /** whether this rule matches the current context evaluator inputs */
  contextMatch: boolean | null;
}

interface ContextInputs {
  tenant: string;
  topic: string;
  capability: string;
}

const SCOPE_PILL_STYLES: Record<ScopeFilter, string> = {
  all: "bg-surface-2 text-foreground",
  global: "bg-cordum/15 text-cordum",
  tenant: "bg-primary/15 text-primary",
  workflow: "bg-info/15 text-info",
};

const DECISION_OPTIONS = [
  { value: "", label: "All decisions" },
  { value: "allow", label: "Allow" },
  { value: "deny", label: "Deny" },
  { value: "require_approval", label: "Require approval" },
  { value: "allow_with_constraints", label: "Allow with constraints" },
  { value: "throttle", label: "Throttle" },
];

// ---------------------------------------------------------------------------
// Exported test-friendly helpers (kept for backward compat with existing tests)
// ---------------------------------------------------------------------------

export function getInputRulesViewModeState(mode: InputRulesPageViewMode) {
  return { showVisual: mode !== "yaml", showYaml: mode !== "visual" };
}

export function getInputRulesAffordances(canEdit: boolean) {
  return {
    canAddRule: canEdit,
    canEditRule: canEdit,
    canReorderRule: canEdit,
    canDeleteRule: canEdit,
    yamlEditable: canEdit,
    drawerReadOnly: !canEdit,
  };
}

function normalizeInputRuleDecision(
  decision: PolicyRule["decision"],
): GlobalPolicyInputDecision {
  switch ((decision ?? "").toLowerCase()) {
    case "allow":
    case "deny":
    case "require_approval":
    case "allow_with_constraints":
    case "throttle":
      return decision.toLowerCase() as GlobalPolicyInputDecision;
    default:
      return "deny";
  }
}

export function getInputRuleEditTarget(
  rule: Pick<PolicyRule, "bundle_id">,
): "bundle" | "drawer" {
  return rule.bundle_id ? "bundle" : "drawer";
}

export function mapPolicyRuleToInputEditorRule(
  rule: PolicyRule,
): GlobalPolicyInputRule {
  const match = rule.match ?? {};
  const constraints = rule.constraints ?? {};
  const budgets = constraints.budgets ?? {};
  const sandbox = constraints.sandbox ?? {};
  const toolchain = constraints.toolchain ?? {};
  const diff = constraints.diff ?? {};
  const mcp = match.mcp ?? {};

  return {
    id: rule.rule_id ?? rule.id,
    decision: normalizeInputRuleDecision(rule.decision),
    reason: rule.reason ?? "",
    match: {
      tenants: match.tenants ?? [],
      topics: match.topics ?? [],
      capabilities: match.capabilities ?? [],
      riskTags: match.risk_tags ?? [],
      requires: match.requires ?? [],
      packIds: match.pack_ids ?? [],
      actorIds: match.actor_ids ?? [],
      actorTypes: match.actor_types ?? [],
      labels: match.labels ?? {},
      secretsPresent: match.secrets_present ?? null,
      mcp: {
        allowServers: mcp.allow_servers ?? [],
        denyServers: mcp.deny_servers ?? [],
        allowTools: mcp.allow_tools ?? [],
        denyTools: mcp.deny_tools ?? [],
        allowResources: mcp.allow_resources ?? [],
        denyResources: mcp.deny_resources ?? [],
        allowActions: mcp.allow_actions ?? [],
        denyActions: mcp.deny_actions ?? [],
      },
    },
    constraints: {
      budgets: {
        maxRuntimeMs: budgets.max_runtime_ms,
        maxRetries: budgets.max_retries,
        maxArtifactBytes: budgets.max_artifact_bytes,
        maxConcurrentJobs: budgets.max_concurrent_jobs,
      },
      sandbox: {
        isolated: sandbox.isolated,
        networkAllowlist: sandbox.network_allowlist ?? [],
        fsReadOnly: sandbox.fs_read_only ?? [],
        fsReadWrite: sandbox.fs_read_write ?? [],
      },
      toolchain: {
        allowedTools: toolchain.allowed_tools ?? [],
        allowedCommands: toolchain.allowed_commands ?? [],
      },
      diff: {
        maxFiles: diff.max_files,
        maxLines: diff.max_lines,
        denyPathGlobs: diff.deny_path_globs ?? [],
      },
      redactionLevel: constraints.redaction_level,
    },
    remediations: [],
    source: rule.source ?? {},
  };
}

// ---------------------------------------------------------------------------
// Scope detection
// ---------------------------------------------------------------------------

function detectScope(
  rule: PolicyRule,
  workflowIds: Set<string>,
): { scope: RuleScope; scopeLabel: string } {
  if (rule.match?.tenants && rule.match.tenants.length > 0) {
    return { scope: "tenant", scopeLabel: rule.match.tenants.join(", ") };
  }
  if (rule.bundle_id && workflowIds.has(rule.bundle_id)) {
    return { scope: "workflow", scopeLabel: rule.bundle_id };
  }
  return { scope: "global", scopeLabel: "" };
}

// ---------------------------------------------------------------------------
// Context matching (client-side glob)
// ---------------------------------------------------------------------------

function simpleGlobMatch(pattern: string, value: string): boolean {
  // Support `*` as wildcard segments and `**` as match-all
  const regex = new RegExp(
    "^" +
      pattern
        .replace(/[.+^${}()|[\]\\]/g, "\\$&")
        .replace(/\*\*/g, "<<GLOBSTAR>>")
        .replace(/\*/g, "[^.]*")
        .replace(/<<GLOBSTAR>>/g, ".*") +
      "$",
  );
  return regex.test(value);
}

function doesRuleMatchContext(rule: PolicyRule, ctx: ContextInputs): boolean {
  const m = rule.match;
  if (!m) return true;
  if (ctx.tenant && m.tenants?.length) {
    if (!m.tenants.includes(ctx.tenant)) return false;
  }
  if (ctx.topic && m.topics?.length) {
    if (!m.topics.some((p) => simpleGlobMatch(p, ctx.topic))) return false;
  }
  if (ctx.capability && m.capabilities?.length) {
    if (!m.capabilities.includes(ctx.capability)) return false;
  }
  return true;
}

// ---------------------------------------------------------------------------
// Page component
// ---------------------------------------------------------------------------

export default function InputRulesPage({ hideHeader }: { hideHeader?: boolean } = {}) {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const policyAccess = usePolicyAccess();
  const affordances = getInputRulesAffordances(policyAccess.canEdit);

  // --- unified data ---
  const { data: rulesData, isLoading: rulesLoading } = usePolicyRules();
  const { data: bundlesData } = usePolicyBundles();
  const { data: workflows } = useWorkflows();

  // --- URL-persisted filters ---
  const scopeFilter = (searchParams.get("scope") ?? "all") as ScopeFilter;
  const decisionFilter = searchParams.get("decision") ?? "";
  const bundleFilter = searchParams.get("bundle") ?? "";
  const searchText = searchParams.get("q") ?? "";

  const searchRef = useRef<HTMLInputElement>(null);
  const searchTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);

  const setFilter = useCallback(
    (key: string, value: string) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        if (value) next.set(key, value);
        else next.delete(key);
        return next;
      }, { replace: true });
    },
    [setSearchParams],
  );

  const onSearchChange = useCallback(
    (value: string) => {
      clearTimeout(searchTimerRef.current);
      searchTimerRef.current = setTimeout(() => setFilter("q", value), 300);
    },
    [setFilter],
  );

  useEffect(() => () => clearTimeout(searchTimerRef.current), []);

  // --- context evaluator ---
  const [contextOpen, setContextOpen] = useState(false);
  const [contextActive, setContextActive] = useState(false);
  const [contextInputs, setContextInputs] = useState<ContextInputs>({
    tenant: "",
    topic: "",
    capability: "",
  });
  const [editorOpen, setEditorOpen] = useState(false);
  const [editingIndex, setEditingIndex] = useState<number | null>(null);

  // --- derived data ---
  const allRules = rulesData?.items ?? [];
  const allBundles = bundlesData?.items ?? [];

  const workflowIdSet = useMemo(
    () => new Set((workflows ?? []).map((w) => w.id)),
    [workflows],
  );
  const workflowNameMap = useMemo(
    () => new Map((workflows ?? []).map((w) => [w.id, w.name || w.id])),
    [workflows],
  );
  const bundleNameMap = useMemo(
    () => new Map(allBundles.map((b) => [b.id, b.name || b.id])),
    [allBundles],
  );

  // available filter options (derived from data)
  const availableTenants = useMemo(() => {
    const set = new Set<string>();
    for (const r of allRules) {
      for (const t of r.match?.tenants ?? []) set.add(t);
    }
    return Array.from(set).sort();
  }, [allRules]);

  const availableBundles = useMemo(() => {
    const set = new Set<string>();
    for (const r of allRules) {
      if (r.bundle_id) set.add(r.bundle_id);
    }
    return Array.from(set).sort();
  }, [allRules]);

  // enrich + filter
  const enrichedRules = useMemo<EnrichedRule[]>(() => {
    const searchLower = searchText.toLowerCase();
    return allRules
      .map((rule): EnrichedRule => {
        const { scope, scopeLabel } = detectScope(rule, workflowIdSet);
        const rawLabel =
          scope === "workflow"
            ? workflowNameMap.get(scopeLabel) ?? scopeLabel
            : scopeLabel;
        return {
          rule,
          scope,
          scopeLabel: rawLabel,
          bundleName: rule.bundle_id
            ? bundleNameMap.get(rule.bundle_id) ?? rule.bundle_id
            : "—",
          contextMatch: contextActive
            ? doesRuleMatchContext(rule, contextInputs)
            : null,
        };
      })
      .filter((e) => {
        if (scopeFilter !== "all" && e.scope !== scopeFilter) return false;
        if (decisionFilter && e.rule.decision !== decisionFilter) return false;
        if (bundleFilter && e.rule.bundle_id !== bundleFilter) return false;
        if (searchLower) {
          const haystack = `${e.rule.id} ${e.rule.name ?? ""} ${e.rule.reason ?? ""} ${e.rule.description ?? ""}`.toLowerCase();
          if (!haystack.includes(searchLower)) return false;
        }
        return true;
      })
      .sort((a, b) => {
        // when context evaluator is active, matching rules float to top
        if (contextActive) {
          if (a.contextMatch && !b.contextMatch) return -1;
          if (!a.contextMatch && b.contextMatch) return 1;
        }
        return 0;
      });
  }, [
    allRules,
    workflowIdSet,
    workflowNameMap,
    bundleNameMap,
    scopeFilter,
    decisionFilter,
    bundleFilter,
    searchText,
    contextActive,
    contextInputs,
  ]);

  const contextMatchCount = contextActive
    ? enrichedRules.filter((e) => e.contextMatch).length
    : 0;
  const editingRule = useMemo(
    () =>
      editingIndex === null
        ? null
        : allRules[editingIndex]
          ? mapPolicyRuleToInputEditorRule(allRules[editingIndex])
          : null,
    [allRules, editingIndex],
  );
  const existingRuleIds = useMemo(
    () =>
      allRules
        .map((rule) => rule.rule_id ?? rule.id)
        .filter((_, index) => index !== editingIndex),
    [allRules, editingIndex],
  );

  // --- loading ---
  if (rulesLoading && allRules.length === 0) {
    return (
      <div className="space-y-3">
        <SkeletonCard />
        <SkeletonCard />
        <SkeletonCard />
      </div>
    );
  }

  // --- render ---
  return (
    <div className="space-y-6">
      {/* Header */}
      {!hideHeader && <PageHeader
        label="Govern"
        title="Input Rules"
        subtitle="All input policy rules across every bundle. See what rules affect any tenant, topic, or workflow."
        actions={
          <StatusBadge variant={affordances.canEditRule ? "healthy" : "muted"}>
            {affordances.canEditRule ? "editor access" : "read-only role"}
          </StatusBadge>
        }
      />}

      <InfoBanner
        variant="cordum"
        title="First-match wins"
        id="input-rules-first-match-help"
      >
        Input rules are evaluated from top to bottom. Keep high-confidence deny rules above broad allow rules.
      </InfoBanner>

      {/* ── Filter bar ── */}
      <div className="space-y-3">
        {/* Scope pills */}
        <div className="flex flex-wrap items-center gap-2">
          {(["all", "global", "tenant", "workflow"] as ScopeFilter[]).map(
            (s) => (
              <button type="button"
                key={s}
                onClick={() => setFilter("scope", s === "all" ? "" : s)}
                className={cn(
                  "rounded-full px-3 py-1 text-xs font-mono transition-colors",
                  scopeFilter === s
                    ? SCOPE_PILL_STYLES[s]
                    : "bg-surface-1 text-muted-foreground hover:bg-surface-2",
                )}
              >
                {s === "all" ? "All" : s.charAt(0).toUpperCase() + s.slice(1)}
                {s === "all" && (
                  <span className="ml-1 text-muted-foreground">
                    ({allRules.length})
                  </span>
                )}
              </button>
            ),
          )}

          <span className="mx-1 h-4 w-px bg-border" />

          {/* Decision filter */}
          <Select
            className="h-7 px-2 text-xs"
            value={decisionFilter}
            onChange={(e) => setFilter("decision", e.target.value)}
            aria-label="Filter by decision"
          >
            {DECISION_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </Select>

          {/* Bundle filter */}
          {availableBundles.length > 1 && (
            <Select
              className="h-7 px-2 text-xs"
              value={bundleFilter}
              onChange={(e) => setFilter("bundle", e.target.value)}
              aria-label="Filter by bundle"
            >
              <option value="">All bundles</option>
              {availableBundles.map((b) => (
                <option key={b} value={b}>
                  {bundleNameMap.get(b) ?? b}
                </option>
              ))}
            </Select>
          )}

          {/* Search */}
          <div className="relative ml-auto w-48">
            <Input
              ref={searchRef}
              type="text"
              icon={<Search className="h-3 w-3" />}
              placeholder="Search rules..."
              defaultValue={searchText}
              onChange={(e) => onSearchChange(e.target.value)}
              className="h-7 text-xs"
              aria-label="Search rules"
            />
          </div>
        </div>

        {/* Tenant / workflow conditional filters */}
        {(scopeFilter === "tenant" || scopeFilter === "all") &&
          availableTenants.length > 0 && (
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <span>Tenant:</span>
              {availableTenants.map((t) => (
                <button type="button"
                  key={t}
                  onClick={() =>
                    setFilter("q", searchText === t ? "" : t)
                  }
                  className={cn(
                    "rounded-full px-2 py-0.5 text-xs font-mono transition-colors",
                    searchText === t
                      ? "bg-primary/15 text-primary"
                      : "bg-surface-2 text-muted-foreground hover:bg-surface-3",
                  )}
                >
                  {t}
                </button>
              ))}
            </div>
          )}
      </div>

      {/* ── Context evaluator (collapsible) ── */}
      <div className="rounded-2xl border border-border bg-surface-1">
        <button type="button"
          onClick={() => setContextOpen(!contextOpen)}
          className="flex w-full items-center gap-2 px-4 py-2.5 text-xs font-semibold text-foreground"
        >
          <Target className="h-3.5 w-3.5 text-cordum" />
          What rules affect...?
          {contextActive && (
            <span className="ml-auto rounded bg-cordum/15 px-2 py-0.5 text-xs font-mono text-cordum">
              {contextMatchCount} of {enrichedRules.length} match
            </span>
          )}
          {contextOpen ? (
            <ChevronUp className={cn("h-3.5 w-3.5 text-muted-foreground", contextActive && "ml-0")} />
          ) : (
            <ChevronDown className={cn("h-3.5 w-3.5 text-muted-foreground", contextActive && "ml-0")} />
          )}
        </button>

        {contextOpen && (
          <div className="border-t border-border px-4 py-3 space-y-3">
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
              <label className="space-y-1">
                <span className="text-xs font-mono text-muted-foreground uppercase">
                  Tenant
                </span>
                <Input
                  type="text"
                  placeholder="e.g. default"
                  value={contextInputs.tenant}
                  onChange={(e) =>
                    setContextInputs((p) => ({ ...p, tenant: e.target.value }))
                  }
                  className="h-8 text-xs"
                  aria-label="Context tenant"
                />
              </label>
              <label className="space-y-1">
                <span className="text-xs font-mono text-muted-foreground uppercase">
                  Topic
                </span>
                <Input
                  type="text"
                  placeholder="e.g. job.fraud-detection.*"
                  value={contextInputs.topic}
                  onChange={(e) =>
                    setContextInputs((p) => ({ ...p, topic: e.target.value }))
                  }
                  className="h-8 text-xs"
                  aria-label="Context topic"
                />
              </label>
              <label className="space-y-1">
                <span className="text-xs font-mono text-muted-foreground uppercase">
                  Capability
                </span>
                <Input
                  type="text"
                  placeholder="e.g. code_exec"
                  value={contextInputs.capability}
                  onChange={(e) =>
                    setContextInputs((p) => ({
                      ...p,
                      capability: e.target.value,
                    }))
                  }
                  className="h-8 text-xs"
                  aria-label="Context capability"
                />
              </label>
            </div>
            <div className="flex items-center gap-2">
              <Button
                size="sm"
                onClick={() => setContextActive(true)}
                disabled={
                  !contextInputs.tenant &&
                  !contextInputs.topic &&
                  !contextInputs.capability
                }
              >
                Find matching rules
              </Button>
              {contextActive && (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    setContextActive(false);
                    setContextInputs({ tenant: "", topic: "", capability: "" });
                  }}
                >
                  <X className="mr-1 h-3 w-3" />
                  Clear
                </Button>
              )}
            </div>
          </div>
        )}
      </div>

      {/* ── Rule list ── */}
      {enrichedRules.length === 0 && !rulesLoading && (
        <EmptyState
          title={scopeFilter !== "all" || decisionFilter || bundleFilter || searchText ? "No rules match filters" : "No input rules configured"}
          description={scopeFilter !== "all" || decisionFilter || bundleFilter || searchText ? "Try adjusting the scope, decision, or search filters." : "Input rules are defined in policy bundles. Create or sync a bundle to get started."}
          action={
            scopeFilter !== "all" || decisionFilter || bundleFilter || searchText ? (
              <Button variant="ghost" size="sm" onClick={() => { setFilter("scope", ""); setFilter("decision", ""); setFilter("bundle", ""); setFilter("q", ""); }}>
                Clear all filters
              </Button>
            ) : (
              <Button variant="outline" size="sm" onClick={() => navigate("/govern/overview?tab=bundles")}>
                View bundles
              </Button>
            )
          }
        />
      )}

      <motion.div 
        initial="hidden"
        animate="visible"
        variants={{
          visible: { transition: { staggerChildren: 0.03 } },
        }}
        className="space-y-3"
      >
        <AnimatePresence>
          {enrichedRules.map((e) => (
            <motion.div
              key={e.rule.id}
              variants={{
                hidden: { opacity: 0, y: 10 },
                visible: { opacity: 1, y: 0 },
              }}
              layout
            >
              <RuleCard
                enriched={e}
                canEdit={affordances.canEditRule}
                onEdit={() => {
                  const bundleId = e.rule.bundle_id;
                  if (bundleId && getInputRuleEditTarget(e.rule) === "bundle") {
                    navigate(`/govern/bundles/${encodeURIComponent(bundleId)}`);
                    return;
                  }
                  const nextEditingIndex = allRules.findIndex(
                    (rule) => rule.id === e.rule.id,
                  );
                  if (nextEditingIndex < 0) return;
                  setEditingIndex(nextEditingIndex);
                  setEditorOpen(true);
                }}
                onSimulate={() =>
                  navigate(
                    buildSimulatorUrl({
                      bundleId: e.rule.bundle_id ?? "",
                    }),
                  )
                }
              />
            </motion.div>
          ))}
        </AnimatePresence>
      </motion.div>

      <InputRuleEditorDrawer
        open={editorOpen}
        readOnly={affordances.drawerReadOnly}
        rule={editingRule}
        nextRuleIndex={allRules.length + 1}
        existingRuleIds={existingRuleIds}
        onClose={() => {
          setEditorOpen(false);
          setEditingIndex(null);
        }}
        onSave={() => {
          setEditorOpen(false);
          setEditingIndex(null);
        }}
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// RuleCard — unified card for the flat rules list
// ---------------------------------------------------------------------------

const SCOPE_BADGE: Record<
  RuleScope,
  { bg: string; text: string; label: string }
> = {
  global: { bg: "bg-cordum/15", text: "text-cordum", label: "Global" },
  tenant: {
    bg: "bg-primary/15",
    text: "text-primary",
    label: "Tenant",
  },
  workflow: { bg: "bg-info/15", text: "text-info", label: "Workflow" },
};

function RuleCard({
  enriched,
  canEdit,
  onEdit,
  onSimulate,
}: {
  enriched: EnrichedRule;
  canEdit: boolean;
  onEdit: () => void;
  onSimulate: () => void;
}) {
  const { rule, scope, scopeLabel, bundleName, contextMatch } = enriched;
  const decision = (rule.decision?.toLowerCase() ?? "allow") as string;
  const scopeInfo = SCOPE_BADGE[scope];
  const match = rule.match;
  const hasMatch =
    match &&
    ((match.topics?.length ?? 0) > 0 ||
      (match.tenants?.length ?? 0) > 0 ||
      (match.capabilities?.length ?? 0) > 0 ||
      (match.risk_tags?.length ?? 0) > 0 ||
      (match.actor_ids?.length ?? 0) > 0);

  const dimmed = contextMatch === false;

  return (
    <InstrumentCard
      accent={getDecisionDisplayVariant(decision)}
      className={cn(
        "transition-all",
        dimmed ? "opacity-40" : ""
      )}
    >
      <div className="pb-1">
        {/* Title row */}
        <div className="flex items-start justify-between gap-3 mb-4">
          <div className="flex flex-wrap items-center gap-2 min-w-0">
            <h3 className="text-sm font-semibold font-display text-foreground truncate">
              {rule.name || rule.id}
            </h3>
            <SafetyDecisionBadge decision={decision} />
            <span
              className={cn(
                "inline-flex items-center px-2 py-0.5 rounded text-xs font-mono",
                scopeInfo.bg,
                scopeInfo.text,
              )}
            >
              {scope === "tenant" && scopeLabel
                ? `Tenant: ${scopeLabel}`
                : scope === "workflow" && scopeLabel
                  ? `Workflow: ${scopeLabel}`
                  : scopeInfo.label}
            </span>
            {!rule.enabled && (
              <span className="text-xs font-mono text-muted-foreground bg-surface-2 px-1.5 py-0.5 rounded">
                DISABLED
              </span>
            )}
          </div>
        </div>

        {/* Description */}
        {(rule.description || rule.reason) && (
          <p className="text-xs text-muted-foreground mb-4 leading-relaxed max-w-2xl">
            {rule.description || rule.reason}
          </p>
        )}

        {/* Match chips */}
        {hasMatch && (
          <div className="surface-inset p-3 mb-3">
            <p className="text-xs font-mono text-muted-foreground uppercase tracking-widest mb-2.5">
              Match
            </p>
            <div className="flex flex-wrap gap-x-5 gap-y-1.5 text-xs font-mono">
              {match?.topics && match?.topics.length > 0 && (
                <span className="text-foreground">
                  <span className="text-muted-foreground/60">Topics:</span>{" "}
                  {match?.topics.join(", ")}
                </span>
              )}
              {match?.tenants && match?.tenants.length > 0 && (
                <span className="text-foreground">
                  <span className="text-muted-foreground/60">Tenants:</span>{" "}
                  {match?.tenants.join(", ")}
                </span>
              )}
              {match?.capabilities && match?.capabilities.length > 0 && (
                <span className="text-foreground">
                  <span className="text-muted-foreground/60">Capabilities:</span>{" "}
                  {match?.capabilities.join(", ")}
                </span>
              )}
              {match?.risk_tags && match?.risk_tags.length > 0 && (
                <span className="text-foreground">
                  <span className="text-muted-foreground/60">Risk Tags:</span>{" "}
                  {match?.risk_tags.join(", ")}
                </span>
              )}
              {match?.actor_ids && match?.actor_ids.length > 0 && (
                <span className="text-foreground">
                  <span className="text-muted-foreground/60">Actor IDs:</span>{" "}
                  {match?.actor_ids.join(", ")}
                </span>
              )}
            </div>
          </div>
        )}

        {/* Footer: bundle source + actions */}
        <div className="flex items-center justify-between pt-1">
          <div className="flex items-center gap-3 text-xs font-mono text-muted-foreground">
            <div className="flex items-center gap-1.5">
              <Package className="h-3 w-3" />
              <span>from {bundleName}</span>
            </div>
            {rule.source &&
              typeof rule.source === "object" &&
              "version" in rule.source &&
              rule.source.version != null && (
                <span className="rounded bg-surface-2 px-1.5 py-0.5">
                  v{String(rule.source.version as string | number)}
                </span>
              )}
            {rule.hitCount24h != null && (
              <span className="px-2 border-l border-border/50">Matches (24h): {rule.hitCount24h}</span>
            )}
          </div>

          {!dimmed && (
            <div className="flex items-center gap-1">
              {canEdit && (
                <button type="button"
                  onClick={onEdit}
                  className="p-1.5 rounded-full text-muted-foreground hover:text-foreground hover:bg-surface-2 transition-colors"
                  title={rule.bundle_id ? "Edit in bundle" : "Edit rule"}
                >
                  <Pencil className="w-3.5 h-3.5" />
                </button>
              )}
              <button type="button"
                onClick={onSimulate}
                className="p-1.5 rounded-full text-muted-foreground hover:text-foreground hover:bg-surface-2 transition-colors"
                title="Simulate"
              >
                <FlaskConical className="w-3.5 h-3.5" />
              </button>
            </div>
          )}
        </div>
      </div>
    </InstrumentCard>
  );
}
