import { useEffect, useMemo, useState } from "react";
import { motion } from "framer-motion";
import {
  Activity,
  Clock3,
  Gauge,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  ShieldAlert,
  Trash2,
  Zap,
} from "lucide-react";
import { ApiError } from "@/api/client";
import { EntitlementGate } from "@/components/EntitlementGate";
import { UpgradePrompt } from "@/components/UpgradePrompt";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { Drawer } from "@/components/ui/Drawer";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { InfoBanner } from "@/components/ui/InfoBanner";
import {
  InstrumentCard,
  InstrumentCardBody,
  InstrumentCardHeader,
} from "@/components/ui/InstrumentCard";
import { Input } from "@/components/ui/Input";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";
import { Select } from "@/components/ui/Select";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { TagInput } from "@/components/ui/TagInput";
import { Textarea } from "@/components/ui/Textarea";
import { useLicense } from "@/hooks/useLicense";
import { usePageTitle } from "@/hooks/usePageTitle";
import { usePolicyAccess } from "@/hooks/usePolicyAccess";
import {
  useCreateVelocityRule,
  useDeleteVelocityRule,
  useUpdateVelocityRule,
  useVelocityRules,
  useVelocityRuleStats,
  type VelocityRule,
  type VelocityRuleInput,
  type VelocityRuleStats,
} from "@/hooks/usePolicies";

const UNLIMITED_LIMIT = -1;

const DECISION_OPTIONS = [
  { value: "require_approval", label: "Require approval" },
  { value: "deny", label: "Deny" },
  { value: "throttle", label: "Throttle" },
  { value: "allow_with_constraints", label: "Allow with constraints" },
  { value: "allow", label: "Allow" },
] as const;

const KEY_OPTIONS = [
  { value: "tenant", label: "tenant" },
  { value: "topic", label: "topic" },
  { value: "actor_id", label: "actor_id" },
  { value: "actor_type", label: "actor_type" },
  { value: "capability", label: "capability" },
  { value: "pack_id", label: "pack_id" },
  { value: "labels.session_id", label: "labels.session_id" },
] as const;

const MATCH_SUGGESTIONS = {
  topics: ["job.auth.login", "job.auth.refresh", "job.payment.charge", "job.risk.evaluate"],
  tenants: ["default", "tenant-a", "tenant-b"],
  riskTags: ["auth", "security", "payments", "fraud"],
};

interface RuleDraft {
  id: string;
  name: string;
  topics: string[];
  tenants: string[];
  riskTags: string[];
  window: string;
  key: string;
  threshold: number;
  decision: VelocityRuleInput["decision"];
  reason: string;
  enabled: boolean;
}

const EMPTY_DRAFT: RuleDraft = {
  id: "",
  name: "",
  topics: [],
  tenants: [],
  riskTags: [],
  window: "1m",
  key: "tenant",
  threshold: 10,
  decision: "require_approval",
  reason: "",
  enabled: true,
};

function mapRuleToDraft(rule: VelocityRule): RuleDraft {
  return {
    id: rule.id,
    name: rule.name,
    topics: rule.match.topics ?? [],
    tenants: rule.match.tenants ?? [],
    riskTags: rule.match.risk_tags ?? [],
    window: rule.window,
    key: rule.key,
    threshold: rule.threshold,
    decision: rule.decision,
    reason: rule.reason,
    enabled: rule.enabled,
  };
}

function formatMatchSummary(rule: VelocityRule): string {
  const parts = [
    rule.match.topics?.length ? `topics: ${rule.match.topics.join(", ")}` : "",
    rule.match.tenants?.length ? `tenants: ${rule.match.tenants.join(", ")}` : "",
    rule.match.risk_tags?.length ? `risk tags: ${rule.match.risk_tags.join(", ")}` : "",
  ].filter(Boolean);
  return parts.join(" • ") || "Applies globally";
}

function formatRelativeTimestamp(timestamp?: string): string {
  if (!timestamp) return "No recent activity";
  const value = new Date(timestamp);
  if (Number.isNaN(value.getTime())) return timestamp;
  return value.toLocaleString();
}

function formatLimit(limit: number): string {
  return limit === UNLIMITED_LIMIT ? "Unlimited" : `${limit}`;
}

function readApiError(error: unknown): string | null {
  if (!(error instanceof ApiError) || !error.body || typeof error.body !== "object") {
    return null;
  }
  const payload = error.body as Record<string, unknown>;
  const detail = payload.message ?? payload.error;
  return typeof detail === "string" ? detail.trim() : null;
}

function buildSparklinePoints(values: number[]): string {
  if (values.length === 0) return "";
  const max = Math.max(...values, 1);
  return values
    .map((value, index) => {
      const x = values.length === 1 ? 0 : (index / (values.length - 1)) * 100;
      const y = 100 - (value / max) * 100;
      return `${x},${y}`;
    })
    .join(" ");
}

function VelocitySparkline({ values }: { values: number[] }) {
  const normalized = values.length > 0 ? values : new Array(24).fill(0);
  const points = buildSparklinePoints(normalized);

  return (
    <div className="h-12 w-28 rounded-2xl border border-border/70 bg-surface-2/40 px-2 py-1.5">
      <svg viewBox="0 0 100 100" preserveAspectRatio="none" className="h-full w-full overflow-visible">
        <polyline
          fill="none"
          stroke="currentColor"
          strokeWidth="4"
          strokeLinejoin="round"
          strokeLinecap="round"
          points={points}
          className="text-cordum"
        />
      </svg>
    </div>
  );
}

export default function VelocityRulesPage({
  hideHeader,
}: {
  hideHeader?: boolean;
} = {}) {
  usePageTitle(hideHeader ? "Policy Studio — Velocity" : "Velocity controls");

  const policyAccess = usePolicyAccess();
  const license = useLicense();
  const rulesQuery = useVelocityRules();
  const statsQuery = useVelocityRuleStats();
  const createRule = useCreateVelocityRule();
  const updateRule = useUpdateVelocityRule();
  const deleteRule = useDeleteVelocityRule();

  const [search, setSearch] = useState("");
  const [editorOpen, setEditorOpen] = useState(false);
  const [draft, setDraft] = useState<RuleDraft>(EMPTY_DRAFT);
  const [editingRuleId, setEditingRuleId] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<VelocityRule | null>(null);

  const rules = rulesQuery.data?.items ?? [];
  const limit = rulesQuery.data?.limit ?? 0;
  const statsById = useMemo(() => {
    const next = new Map<string, VelocityRuleStats>();
    for (const item of statsQuery.data?.items ?? []) {
      next.set(item.id, item);
    }
    return next;
  }, [statsQuery.data?.items]);

  const mergedRules = useMemo(
    () =>
      rules.map((rule) => ({
        rule,
        stats: statsById.get(rule.id),
      })),
    [rules, statsById],
  );

  const filteredRules = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!query) return mergedRules;
    return mergedRules.filter(({ rule }) =>
      [
        rule.id,
        rule.name,
        rule.key,
        rule.reason,
        rule.match.topics.join(" "),
        rule.match.tenants.join(" "),
        rule.match.risk_tags.join(" "),
      ]
        .join(" ")
        .toLowerCase()
        .includes(query),
    );
  }, [mergedRules, search]);

  const totalHits24h = useMemo(
    () => (statsQuery.data?.items ?? []).reduce((sum, item) => sum + item.hitCount24h, 0),
    [statsQuery.data?.items],
  );
  const exceededBuckets = useMemo(
    () => (statsQuery.data?.items ?? []).reduce((sum, item) => sum + item.exceededBuckets, 0),
    [statsQuery.data?.items],
  );
  const enabledRules = rules.filter((rule) => rule.enabled).length;
  const limitReached = limit !== UNLIMITED_LIMIT && limit > 0 && rules.length >= limit;
  const velocityEntitled = license.data?.entitlements.velocityRules === true;
  const canCreate =
    velocityEntitled &&
    policyAccess.canEdit &&
    (limit === UNLIMITED_LIMIT || (limit > 0 && rules.length < limit));

  useEffect(() => {
    if (!editorOpen) {
      setFormError(null);
    }
  }, [editorOpen]);

  if ((rulesQuery.isLoading && !rulesQuery.data) || (statsQuery.isLoading && !statsQuery.data)) {
    return (
      <div className="space-y-6">
        {!hideHeader && (
          <PageHeader
            label="Govern"
            title="Velocity controls"
            subtitle="Create sliding-window policy fragments, monitor bucket pressure, and tune approval or deny actions without touching kernel logic."
          />
        )}
        <div className="grid gap-4 xl:grid-cols-4">
          <SkeletonCard />
          <SkeletonCard />
          <SkeletonCard />
          <SkeletonCard />
        </div>
        <SkeletonCard />
      </div>
    );
  }

  if ((rulesQuery.isError && !rulesQuery.data) || (statsQuery.isError && !statsQuery.data)) {
    return (
      <ErrorBanner
        title="Unable to load velocity rules"
        message={
          (rulesQuery.error instanceof Error ? rulesQuery.error.message : "") ||
          (statsQuery.error instanceof Error ? statsQuery.error.message : "") ||
          "Failed to load velocity-rule configuration."
        }
        onRetry={() => {
          void rulesQuery.refetch();
          void statsQuery.refetch();
        }}
      />
    );
  }

  const limitMetric =
    limit > 0
      ? {
          current: rules.length,
          allowed: limit,
        }
      : undefined;

  const openCreateDrawer = () => {
    setEditingRuleId(null);
    setDraft(EMPTY_DRAFT);
    setEditorOpen(true);
  };

  const openEditDrawer = (rule: VelocityRule) => {
    setEditingRuleId(rule.id);
    setDraft(mapRuleToDraft(rule));
    setEditorOpen(true);
  };

  const handleSave = async () => {
    setFormError(null);
    const payload: VelocityRuleInput = {
      id: draft.id.trim(),
      name: draft.name.trim(),
      match: {
        topics: draft.topics,
        tenants: draft.tenants,
        risk_tags: draft.riskTags,
      },
      window: draft.window.trim(),
      key: draft.key.trim(),
      threshold: Number(draft.threshold),
      decision: draft.decision,
      reason: draft.reason.trim(),
      enabled: draft.enabled,
    };

    try {
      if (editingRuleId) {
        await updateRule.mutateAsync(payload);
      } else {
        await createRule.mutateAsync(payload);
      }
      setEditorOpen(false);
    } catch (error) {
      setFormError(
        readApiError(error) ??
          (error instanceof Error ? error.message : "Failed to save velocity rule"),
      );
    }
  };

  return (
    <div className="space-y-6 animate-rise">
      {!hideHeader && (
        <PageHeader
          label="Govern"
          title="Velocity controls"
          subtitle="Manage live sliding-window rule fragments, inspect retained activity, and tune escalation thresholds from one operator surface."
          actions={
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  void rulesQuery.refetch();
                  void statsQuery.refetch();
                }}
              >
                <RefreshCw className="h-3.5 w-3.5" />
                Refresh
              </Button>
              <Button size="sm" onClick={openCreateDrawer} disabled={!canCreate}>
                <Plus className="h-3.5 w-3.5" />
                New rule
              </Button>
            </div>
          }
        />
      )}

      {hideHeader && (
        <div className="flex flex-wrap items-center justify-end gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              void rulesQuery.refetch();
              void statsQuery.refetch();
            }}
          >
            <RefreshCw className="h-3.5 w-3.5" />
            Refresh
          </Button>
          <Button size="sm" onClick={openCreateDrawer} disabled={!canCreate}>
            <Plus className="h-3.5 w-3.5" />
            New rule
          </Button>
        </div>
      )}

      {policyAccess.isReadOnly && (
        <InfoBanner variant="info" title="View-only rule access">
          You can inspect velocity rules and recent activity, but only policy editors can create,
          update, or delete rule fragments.
        </InfoBanner>
      )}

      <EntitlementGate
        entitlement="velocityRules"
        label="Velocity rules"
        description="Velocity-rule fragments and live counters require a licensed deployment."
      >
        <div className="space-y-6">
          {limitReached && (
            <UpgradePrompt
              forceVisible
              label="Velocity rules"
              metric={limitMetric}
              plan={license.data?.plan}
              title={`Velocity rule limit reached (${rules.length}/${formatLimit(limit)})`}
              description="This deployment has reached its configured velocity-rule cap. Upgrade to create additional rule fragments without deleting existing protections."
            />
          )}

          <motion.div 
            initial="hidden"
            animate="visible"
            variants={{
              visible: { transition: { staggerChildren: 0.05 } },
            }}
            className="grid gap-4 md:grid-cols-2 xl:grid-cols-4"
          >
            <motion.div variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}>
              <InstrumentCard accent="governance">
                <InstrumentCardHeader title="Configured rules" icon={<Zap className="h-4 w-4" />} />
                <InstrumentCardBody>
                  <p className="text-3xl font-display font-semibold text-foreground">{rules.length}</p>
                  <p className="mt-1 text-xs text-muted-foreground">
                    Limit: {limit === UNLIMITED_LIMIT ? "Unlimited" : `${rules.length}/${limit}`}
                  </p>
                </InstrumentCardBody>
              </InstrumentCard>
            </motion.div>

            <motion.div variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}>
              <InstrumentCard accent="healthy">
                <InstrumentCardHeader title="Enabled fragments" icon={<Gauge className="h-4 w-4" />} />
                <InstrumentCardBody>
                  <p className="text-3xl font-display font-semibold text-foreground">{enabledRules}</p>
                  <p className="mt-1 text-xs text-muted-foreground">
                    Disabled fragments stay editable but do not feed active bundle evaluation.
                  </p>
                </InstrumentCardBody>
              </InstrumentCard>
            </motion.div>

            <motion.div variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}>
              <InstrumentCard accent={totalHits24h > 0 ? "cordum" : "muted"}>
                <InstrumentCardHeader title="Retained hits (24h)" icon={<Activity className="h-4 w-4" />} />
                <InstrumentCardBody>
                  <p className="text-3xl font-display font-semibold text-foreground">
                    {totalHits24h.toLocaleString()}
                  </p>
                  <p className="mt-1 text-xs text-muted-foreground">
                    Aggregated from the existing velocity Redis buckets.
                  </p>
                </InstrumentCardBody>
              </InstrumentCard>
            </motion.div>

            <motion.div variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}>
              <InstrumentCard accent={exceededBuckets > 0 ? "warning" : "muted"}>
                <InstrumentCardHeader title="Buckets over threshold" icon={<ShieldAlert className="h-4 w-4" />} />
                <InstrumentCardBody>
                  <p className="text-3xl font-display font-semibold text-foreground">{exceededBuckets}</p>
                  <p className="mt-1 text-xs text-muted-foreground">
                    Active bucket windows currently above their configured rule threshold.
                  </p>
                </InstrumentCardBody>
              </InstrumentCard>
            </motion.div>
          </motion.div>

          <section className="instrument-card">
            <div className="flex flex-col gap-3 border-b border-border/50 pb-4 md:flex-row md:items-center md:justify-between">
              <div>
                <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
                  Active fragments
                </p>
                <h2 className="mt-1 text-lg font-display font-semibold text-foreground">
                  Rule inventory and retained activity
                </h2>
              </div>
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                <Input
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  placeholder="Search rule name, key, topic, tenant, or reason"
                  icon={<Search className="h-4 w-4" />}
                  className="min-w-[280px]"
                />
                {statsQuery.data?.generatedAt && (
                  <div className="rounded-full border border-border/60 px-3 py-2 text-xs text-muted-foreground">
                    Stats refreshed {formatRelativeTimestamp(statsQuery.data.generatedAt)}
                  </div>
                )}
              </div>
            </div>

            {filteredRules.length === 0 ? (
              <EmptyState
                icon={<Zap className="h-5 w-5" />}
                title={rules.length === 0 ? "No velocity rules configured" : "No rules match the current search"}
                description={
                  rules.length === 0
                    ? "Create a rule fragment to enforce per-key velocity thresholds with approval, deny, or throttle actions."
                    : "Try a different search term or clear the current filter."
                }
                action={
                  rules.length === 0 && canCreate ? (
                    <Button size="sm" onClick={openCreateDrawer}>
                      <Plus className="h-3.5 w-3.5" />
                      Create rule
                    </Button>
                  ) : undefined
                }
              />
            ) : (
              <div className="overflow-x-auto">
                <table className="min-w-full border-separate border-spacing-y-3">
                  <thead>
                    <tr className="text-left text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                      <th className="px-2 pb-1">Rule</th>
                      <th className="px-2 pb-1">Match</th>
                      <th className="px-2 pb-1">Window</th>
                      <th className="px-2 pb-1">Decision</th>
                      <th className="px-2 pb-1">24h activity</th>
                      <th className="px-2 pb-1">Status</th>
                      <th className="px-2 pb-1 text-right">Actions</th>
                    </tr>
                  </thead>
                  <motion.tbody
                    initial="hidden"
                    animate="visible"
                    variants={{
                      visible: { transition: { staggerChildren: 0.03 } },
                    }}
                  >
                    {filteredRules.map(({ rule, stats }) => (
                      <motion.tr 
                        key={rule.id} 
                        variants={{
                          hidden: { opacity: 0, x: -4 },
                          visible: { opacity: 1, x: 0 },
                        }}
                        className="rounded-3xl bg-surface-1/70 align-top shadow-soft"
                      >
                        <td className="rounded-l-3xl px-2 py-4">
                          <div className="space-y-2 rounded-3xl border border-border/60 bg-surface-1 px-4 py-3">
                            <div className="flex items-start gap-3">
                              <div className="rounded-2xl bg-cordum/12 p-2 text-cordum">
                                <Zap className="h-4 w-4" />
                              </div>
                              <div className="min-w-0">
                                <div className="flex flex-wrap items-center gap-2">
                                  <p className="text-sm font-semibold text-foreground">{rule.name}</p>
                                  <code className="rounded-full bg-surface-2 px-2 py-0.5 text-[11px] text-muted-foreground">
                                    {rule.id}
                                  </code>
                                </div>
                                <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                                  {rule.reason}
                                </p>
                              </div>
                            </div>
                            <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                              <span>
                                Key: <span className="font-mono text-foreground">{rule.key}</span>
                              </span>
                              <span>
                                Threshold: <span className="font-semibold text-foreground">{rule.threshold}</span>
                              </span>
                            </div>
                          </div>
                        </td>
                        <td className="px-2 py-4">
                          <div className="rounded-3xl border border-border/60 bg-surface-1 px-4 py-3 text-sm text-muted-foreground">
                            {formatMatchSummary(rule)}
                          </div>
                        </td>
                        <td className="px-2 py-4">
                          <div className="rounded-3xl border border-border/60 bg-surface-1 px-4 py-3 text-sm text-foreground">
                            <div className="flex items-center gap-2">
                              <Clock3 className="h-4 w-4 text-muted-foreground" />
                              <span>{rule.window}</span>
                            </div>
                            <p className="mt-2 text-xs text-muted-foreground">
                              Current max bucket: {stats?.currentWindowMax ?? 0}
                            </p>
                          </div>
                        </td>
                        <td className="px-2 py-4">
                          <div className="rounded-3xl border border-border/60 bg-surface-1 px-4 py-3">
                            <SafetyDecisionBadge decision={rule.decision} />
                          </div>
                        </td>
                        <td className="px-2 py-4">
                          <div className="rounded-3xl border border-border/60 bg-surface-1 px-4 py-3">
                            <div className="flex items-center gap-3">
                              <VelocitySparkline values={stats?.hourlyHits ?? []} />
                              <div>
                                <p className="text-sm font-semibold text-foreground">
                                  {(stats?.hitCount24h ?? 0).toLocaleString()} hits
                                </p>
                                <p className="text-xs text-muted-foreground">
                                  Current window: {(stats?.currentWindowCount ?? 0).toLocaleString()}
                                </p>
                                <p className="mt-1 text-xs text-muted-foreground">
                                  Last seen: {formatRelativeTimestamp(stats?.lastTriggered)}
                                </p>
                              </div>
                            </div>
                          </div>
                        </td>
                        <td className="px-2 py-4">
                          <div className="flex flex-col gap-2 rounded-3xl border border-border/60 bg-surface-1 px-4 py-3">
                            <StatusBadge variant={rule.enabled ? "healthy" : "muted"} dot>
                              {rule.enabled ? "Enabled" : "Disabled"}
                            </StatusBadge>
                            <StatusBadge
                              variant={(stats?.exceededBuckets ?? 0) > 0 ? "warning" : "info"}
                              dot
                            >
                              {(stats?.exceededBuckets ?? 0) > 0
                                ? `${stats?.exceededBuckets ?? 0} bucket(s) over threshold`
                                : "Within threshold"}
                            </StatusBadge>
                          </div>
                        </td>
                        <td className="rounded-r-3xl px-2 py-4">
                          <div className="flex justify-end gap-2 rounded-3xl border border-border/60 bg-surface-1 px-4 py-3">
                            <Button
                              variant="outline"
                              size="sm"
                              onClick={() => openEditDrawer(rule)}
                              disabled={!policyAccess.canEdit}
                            >
                              <Pencil className="h-3.5 w-3.5" />
                              Edit
                            </Button>
                            <Button
                              variant="danger"
                              size="sm"
                              onClick={() => setDeleteTarget(rule)}
                              disabled={!policyAccess.canEdit}
                            >
                              <Trash2 className="h-3.5 w-3.5" />
                              Delete
                            </Button>
                          </div>
                        </td>
                      </motion.tr>
                    ))}
                  </motion.tbody>
                </table>
              </div>
            )}
          </section>
        </div>
      </EntitlementGate>

      <Drawer
        open={editorOpen}
        onClose={() => setEditorOpen(false)}
        size="xl"
        label={editingRuleId ? "Edit velocity rule" : "Create velocity rule"}
      >
        <div className="space-y-6">
          <div className="flex items-start justify-between gap-4">
            <div>
              <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
                {editingRuleId ? "Edit fragment" : "New fragment"}
              </p>
              <h2 className="mt-1 text-xl font-display font-semibold text-foreground">
                {editingRuleId ? draft.name || draft.id : "Create velocity rule"}
              </h2>
              <p className="mt-2 max-w-xl text-sm text-muted-foreground">
                Use supported key expressions like <code className="font-mono">tenant</code>,
                <code className="ml-1 font-mono">topic</code>, or
                <code className="ml-1 font-mono">labels.session_id</code>. Threshold
                evaluation remains in the safety kernel; this editor only manages the bundle
                fragment.
              </p>
            </div>
            <StatusBadge variant={draft.enabled ? "healthy" : "muted"} dot>
              {draft.enabled ? "Enabled" : "Disabled"}
            </StatusBadge>
          </div>

          {formError && (
            <InfoBanner variant="error" title="Unable to save rule">
              {formError}
            </InfoBanner>
          )}

          <div className="grid gap-4 md:grid-cols-2">
            <label className="space-y-2">
              <span className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                Rule ID
              </span>
              <Input
                value={draft.id}
                onChange={(event) => setDraft((current) => ({ ...current, id: event.target.value }))}
                placeholder="login-burst"
                disabled={Boolean(editingRuleId)}
              />
            </label>
            <label className="space-y-2">
              <span className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                Name
              </span>
              <Input
                value={draft.name}
                onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value }))}
                placeholder="Login burst guard"
              />
            </label>
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <label className="space-y-2">
              <span className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                Window
              </span>
              <Input
                value={draft.window}
                onChange={(event) => setDraft((current) => ({ ...current, window: event.target.value }))}
                placeholder="1m"
              />
            </label>
            <label className="space-y-2">
              <span className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                Threshold
              </span>
              <Input
                type="number"
                min={1}
                value={draft.threshold}
                onChange={(event) =>
                  setDraft((current) => ({
                    ...current,
                    threshold: Number(event.target.value) || 0,
                  }))
                }
              />
            </label>
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <label className="space-y-2">
              <span className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                Key expression
              </span>
              <div className="space-y-2">
                <Select
                  value={KEY_OPTIONS.some((option) => option.value === draft.key) ? draft.key : ""}
                  onChange={(event) => {
                    if (event.target.value) {
                      setDraft((current) => ({ ...current, key: event.target.value }));
                    }
                  }}
                  options={KEY_OPTIONS.map((option) => ({
                    value: option.value,
                    label: option.label,
                  }))}
                />
                <Input
                  value={draft.key}
                  onChange={(event) => setDraft((current) => ({ ...current, key: event.target.value }))}
                  placeholder="tenant or labels.session_id"
                />
              </div>
            </label>
            <label className="space-y-2">
              <span className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                Decision
              </span>
              <Select
                value={draft.decision}
                onChange={(event) =>
                  setDraft((current) => ({
                    ...current,
                    decision: event.target.value as VelocityRuleInput["decision"],
                  }))
                }
                options={DECISION_OPTIONS.map((option) => ({
                  value: option.value,
                  label: option.label,
                }))}
              />
            </label>
          </div>

          <div className="grid gap-4 xl:grid-cols-3">
            <div className="space-y-2">
              <span className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                Topics
              </span>
              <TagInput
                value={draft.topics}
                onChange={(topics) => setDraft((current) => ({ ...current, topics }))}
                suggestions={MATCH_SUGGESTIONS.topics}
                placeholder="Add topic glob"
              />
            </div>
            <div className="space-y-2">
              <span className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                Tenants
              </span>
              <TagInput
                value={draft.tenants}
                onChange={(tenants) => setDraft((current) => ({ ...current, tenants }))}
                suggestions={MATCH_SUGGESTIONS.tenants}
                placeholder="Add tenant"
              />
            </div>
            <div className="space-y-2">
              <span className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                Risk tags
              </span>
              <TagInput
                value={draft.riskTags}
                onChange={(riskTags) => setDraft((current) => ({ ...current, riskTags }))}
                suggestions={MATCH_SUGGESTIONS.riskTags}
                placeholder="Add risk tag"
              />
            </div>
          </div>

          <label className="space-y-2">
            <span className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
              Reason
            </span>
            <Textarea
              value={draft.reason}
              onChange={(event) => setDraft((current) => ({ ...current, reason: event.target.value }))}
              rows={4}
              placeholder="Describe why this rule should fire when the velocity threshold is exceeded."
            />
          </label>

          <label className="flex items-center gap-3 rounded-2xl border border-border/60 bg-surface-1 px-4 py-3 text-sm text-foreground">
            <input
              type="checkbox"
              checked={draft.enabled}
              onChange={(event) => setDraft((current) => ({ ...current, enabled: event.target.checked }))}
              className="h-4 w-4 rounded border-border text-cordum focus:ring-cordum/40"
            />
            Keep this fragment enabled after save
          </label>

          <div className="flex items-center justify-end gap-2 border-t border-border/50 pt-4">
            <Button variant="outline" onClick={() => setEditorOpen(false)}>
              Cancel
            </Button>
            <Button
              onClick={handleSave}
              loading={createRule.isPending || updateRule.isPending}
              disabled={!policyAccess.canEdit}
            >
              {editingRuleId ? "Save changes" : "Create rule"}
            </Button>
          </div>
        </div>
      </Drawer>

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!deleteTarget) return;
          deleteRule.mutate(
            { id: deleteTarget.id },
            {
              onSuccess: () => setDeleteTarget(null),
            },
          );
        }}
        title={deleteTarget ? `Delete ${deleteTarget.name}?` : "Delete velocity rule"}
        description="This removes the velocity bundle fragment from the active policy config. Existing Redis bucket data will age out naturally."
        confirmLabel="Delete rule"
        cancelLabel="Keep rule"
        variant="destructive"
        isPending={deleteRule.isPending}
      />
    </div>
  );
}
