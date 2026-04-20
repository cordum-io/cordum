/*
 * DESIGN: "Control Surface" — Output Safety
 * Output quarantine queue, findings detail, redaction preview, policy rules
 */
import { useState, useMemo } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonTable, SkeletonCard } from "@/components/ui/Skeleton";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import {
  ShieldAlert, CheckCircle2, XCircle, Save,
  ChevronDown, ChevronRight, Shield,
  FileText, Lock, Unlock, AlertTriangle,
} from "lucide-react";
import { cn, formatRelativeTime } from "@/lib/utils";
import type { Job, OutputFinding } from "@/api/types";
import {
  useQuarantinedJobs,
  useReleaseQuarantinedJob,
  useConfirmQuarantine,
  useOutputPolicyConfig,
  useUpdateOutputPolicy,
  useOutputPolicyStats,
} from "@/hooks/useOutputPolicy";
import { useOutputRules } from "@/hooks/useOutputRules";

function maxSeverity(findings?: OutputFinding[]): "high" | "medium" | "low" {
  if (!findings || findings.length === 0) return "low";
  if (findings.some(f => f.severity === "high" || f.severity === "critical")) return "high";
  if (findings.some(f => f.severity === "medium")) return "medium";
  return "low";
}

export default function OutputSafetyPage() {
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState<"quarantine" | "rules" | "settings">("quarantine");
  const [expandedItem, setExpandedItem] = useState<string | null>(null);
  const [releaseTarget, setReleaseTarget] = useState<Job | null>(null);
  const [severityFilter, setSeverityFilter] = useState("all");
  const [selectedItems, setSelectedItems] = useState<Set<string>>(new Set());

  const { data: quarantineData, isLoading: quarantineLoading, error: quarantineError } = useQuarantinedJobs();
  const quarantined = quarantineData?.items ?? [];

  const { data: outputRules, isLoading: rulesLoading } = useOutputRules();
  const rules = outputRules ?? [];

  const { data: policyConfig, isLoading: configLoading } = useOutputPolicyConfig();
  const updatePolicyMutation = useUpdateOutputPolicy();

  const { data: stats, isLoading: statsLoading } = useOutputPolicyStats();

  const releaseMutation = useReleaseQuarantinedJob();
  const discardMutation = useConfirmQuarantine();

  const itemsWithSeverity = useMemo(
    () => quarantined.map(job => ({ job, severity: maxSeverity(job.output_safety?.findings) })),
    [quarantined],
  );

  const filtered = useMemo(() => {
    if (severityFilter === "all") return itemsWithSeverity;
    return itemsWithSeverity.filter(item => item.severity === severityFilter);
  }, [itemsWithSeverity, severityFilter]);

  const highSeverityCount = useMemo(
    () => itemsWithSeverity.filter(i => i.severity === "high").length,
    [itemsWithSeverity],
  );
  const quarantineCount = useMemo(
    () => quarantined.filter(j => j.output_safety?.decision === "QUARANTINE").length,
    [quarantined],
  );
  const redactCount = useMemo(
    () => quarantined.filter(j => j.output_safety?.decision === "REDACT").length,
    [quarantined],
  );
  const activeRulesCount = useMemo(() => rules.filter(r => r.enabled).length, [rules]);

  const severityColor = (s: string): BadgeVariant => s === "high" ? "danger" : s === "medium" ? "warning" : "info";

  const toggleSelect = (id: string) => {
    setSelectedItems(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const handleBulkRelease = () => {
    for (const id of selectedItems) releaseMutation.mutate(id);
    setSelectedItems(new Set());
  };

  const handleBulkDiscard = () => {
    for (const id of selectedItems) discardMutation.mutate(id);
    setSelectedItems(new Set());
  };

  const sensitiveDataEnabled = policyConfig?.enabled ?? false;
  const autoQuarantine = policyConfig?.failureAction === "deny";

  const handleToggleSensitiveData = () => {
    if (!policyConfig) return;
    updatePolicyMutation.mutate({ ...policyConfig, enabled: !sensitiveDataEnabled });
  };

  const handleToggleAutoQuarantine = () => {
    if (!policyConfig) return;
    updatePolicyMutation.mutate({
      ...policyConfig,
      failureAction: autoQuarantine ? "allow" : "deny",
    });
  };

  const tabs = [
    { id: "quarantine" as const, label: "Quarantine", icon: ShieldAlert, count: quarantined.length },
    { id: "rules" as const, label: "Output Rules", icon: Shield, count: rules.length },
    { id: "settings" as const, label: "Settings", icon: Save },
  ];

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader
        label="Govern"
        title="Output Safety"
        subtitle={`${quarantined.length} quarantined · ${activeRulesCount} active rules`}
      />

      {/* KPI Row */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <div className={cn("instrument-card p-4", highSeverityCount > 0 && "status-danger")}>
          <div className="flex items-center justify-between mb-2">
            <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest">High Severity</span>
            <AlertTriangle className="w-4 h-4 text-red-400" />
          </div>
          <span className="font-mono text-2xl font-bold text-red-400">
            {quarantineLoading ? "—" : highSeverityCount}
          </span>
        </div>
        <div className="instrument-card p-4">
          <div className="flex items-center justify-between mb-2">
            <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Quarantined</span>
            <Lock className="w-4 h-4 text-amber-400" />
          </div>
          <span className="font-mono text-2xl font-bold text-foreground">
            {quarantineLoading ? "—" : quarantineCount}
          </span>
        </div>
        <div className="instrument-card p-4">
          <div className="flex items-center justify-between mb-2">
            <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Redacted</span>
            <FileText className="w-4 h-4 text-blue-400" />
          </div>
          <span className="font-mono text-2xl font-bold text-foreground">
            {quarantineLoading ? "—" : redactCount}
          </span>
        </div>
        <div className="instrument-card p-4">
          <div className="flex items-center justify-between mb-2">
            <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Active Rules</span>
            <Shield className="w-4 h-4 text-cordum" />
          </div>
          <span className="font-mono text-2xl font-bold text-foreground">
            {rulesLoading ? "—" : activeRulesCount}
          </span>
        </div>
      </div>

      {/* Tabs */}
      <div className="flex items-center gap-4 border-b border-border">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={cn(
              "pb-2 text-sm font-medium border-b-2 transition-colors flex items-center gap-1.5",
              activeTab === tab.id ? "border-cordum text-cordum" : "border-transparent text-muted-foreground hover:text-foreground"
            )}
          >
            <tab.icon className="w-3.5 h-3.5" />
            {tab.label}
            {tab.count !== undefined && (
              <span className="text-[10px] font-mono bg-surface-2 px-1.5 py-0.5 rounded">{tab.count}</span>
            )}
          </button>
        ))}
      </div>

      {/* Quarantine Tab */}
      {activeTab === "quarantine" && (
        <>
          {/* Filters + Bulk Actions */}
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <div className="flex items-center gap-1 bg-surface-1 border border-border rounded-md p-0.5">
                {["all", "high", "medium", "low"].map((s) => (
                  <button
                    key={s}
                    onClick={() => setSeverityFilter(s)}
                    className={cn(
                      "px-3 py-1 text-xs font-medium rounded transition-colors capitalize",
                      severityFilter === s ? "bg-cordum/10 text-cordum" : "text-muted-foreground hover:text-foreground"
                    )}
                  >
                    {s}
                  </button>
                ))}
              </div>
            </div>
            {selectedItems.size > 0 && (
              <div className="flex items-center gap-2">
                <span className="text-xs text-muted-foreground">{selectedItems.size} selected</span>
                <Button variant="outline" size="sm" onClick={handleBulkRelease} disabled={releaseMutation.isPending}>
                  <Unlock className="w-3 h-3 mr-1" />
                  Release
                </Button>
                <Button variant="danger" size="sm" onClick={handleBulkDiscard} disabled={discardMutation.isPending}>
                  <XCircle className="w-3 h-3 mr-1" />
                  Discard
                </Button>
              </div>
            )}
          </div>

          {quarantineLoading ? (
            <SkeletonTable rows={4} />
          ) : quarantineError ? (
            <div className="instrument-card p-8 text-center">
              <AlertTriangle className="w-8 h-8 text-red-400 mx-auto mb-3" />
              <p className="text-sm text-foreground font-medium mb-1">Failed to load quarantine queue</p>
              <p className="text-xs text-muted-foreground mb-4">
                {quarantineError instanceof Error ? quarantineError.message : "An unexpected error occurred"}
              </p>
              <Button variant="outline" size="sm" onClick={() => queryClient.invalidateQueries({ queryKey: ["jobs"] })}>
                Retry
              </Button>
            </div>
          ) : filtered.length === 0 ? (
            <EmptyState icon={<ShieldAlert className="w-8 h-8" />} title="Quarantine empty" description="No outputs flagged by safety checks" />
          ) : (
            <div className="space-y-2">
              {filtered.map(({ job: item, severity }) => {
                const isExpanded = expandedItem === item.id;
                const decision = item.output_safety?.decision ?? "QUARANTINE";
                const findings = item.output_safety?.findings ?? [];
                const ruleId = item.output_safety?.rule_id;
                const reason = item.output_safety?.reason ?? item.errorMessage ?? "Output flagged by safety scanner";
                const scanner = findings[0]?.scanner;
                return (
                  <div key={item.id} className="instrument-card overflow-hidden">
                    <div className="flex items-center gap-3 px-5 py-3 hover:bg-surface-1 transition-colors">
                      {/* Checkbox */}
                      <input
                        type="checkbox"
                        checked={selectedItems.has(item.id)}
                        onChange={() => toggleSelect(item.id)}
                        className="w-3.5 h-3.5 rounded border-border accent-cordum"
                      />

                      {/* Expand toggle */}
                      <button onClick={() => setExpandedItem(isExpanded ? null : item.id)} className="shrink-0">
                        {isExpanded ? <ChevronDown className="w-4 h-4 text-muted-foreground" /> : <ChevronRight className="w-4 h-4 text-muted-foreground" />}
                      </button>

                      {/* Content */}
                      <div className="flex-1 min-w-0 cursor-pointer" onClick={() => setExpandedItem(isExpanded ? null : item.id)}>
                        <div className="flex items-center gap-2 mb-0.5">
                          <span className="font-mono text-xs text-cordum">{item.id.slice(0, 12)}</span>
                          <StatusBadge variant={severityColor(severity)}>{severity}</StatusBadge>
                          <span className={cn(
                            "px-1.5 py-0.5 rounded font-mono text-[10px] font-semibold",
                            decision === "QUARANTINE" ? "bg-amber-400/10 text-amber-400" : "bg-blue-400/10 text-blue-400"
                          )}>
                            {decision}
                          </span>
                        </div>
                        <p className="text-xs text-foreground">{reason}</p>
                        <p className="text-[10px] text-muted-foreground mt-0.5">
                          {scanner && <span className="font-mono">{scanner}</span>}
                          {scanner && " · "}
                          {item.updatedAt ? formatRelativeTime(item.updatedAt) : "—"}
                        </p>
                      </div>

                      {/* Actions */}
                      <div className="flex items-center gap-1 shrink-0">
                        <button
                          onClick={() => setReleaseTarget(item)}
                          className="p-1.5 rounded hover:bg-emerald-500/10 transition-colors"
                          title="Release"
                          disabled={releaseMutation.isPending}
                        >
                          <CheckCircle2 className="w-3.5 h-3.5 text-emerald-400" />
                        </button>
                        <button
                          onClick={() => discardMutation.mutate(item.id)}
                          className="p-1.5 rounded hover:bg-red-500/10 transition-colors"
                          title="Discard"
                          disabled={discardMutation.isPending}
                        >
                          <XCircle className="w-3.5 h-3.5 text-red-400" />
                        </button>
                      </div>
                    </div>

                    {/* Expanded detail */}
                    <AnimatePresence>
                      {isExpanded && (
                        <motion.div
                          initial={{ height: 0, opacity: 0 }}
                          animate={{ height: "auto", opacity: 1 }}
                          exit={{ height: 0, opacity: 0 }}
                          className="border-t border-border bg-surface-0"
                        >
                          <div className="p-5 space-y-4">
                            {/* Reason */}
                            <div>
                              <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest mb-2">Reason</p>
                              <div className="bg-surface-1 rounded-lg border border-border p-3 font-mono text-xs text-foreground whitespace-pre-wrap">
                                {reason}
                              </div>
                            </div>

                            {/* Redacted preview */}
                            {item.output_safety?.redacted_ptr && (
                              <div>
                                <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest mb-2">Redacted Output</p>
                                <div className="bg-surface-1 rounded-lg border border-blue-400/20 p-3 font-mono text-xs text-foreground whitespace-pre-wrap">
                                  {item.output_safety.redacted_ptr}
                                </div>
                              </div>
                            )}

                            {/* Findings */}
                            {findings.length > 0 && (
                              <div>
                                <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest mb-2">Findings ({findings.length})</p>
                                <div className="space-y-2">
                                  {findings.map((finding, idx) => (
                                    <div key={idx} className="rounded-lg border border-border bg-surface-1 p-3">
                                      <div className="flex items-center gap-2 mb-1">
                                        <StatusBadge variant={severityColor(finding.severity)}>{finding.severity}</StatusBadge>
                                        <span className="font-mono text-xs text-foreground font-medium">{finding.type}</span>
                                        {finding.scanner && <span className="text-[10px] text-muted-foreground font-mono">{finding.scanner}</span>}
                                      </div>
                                      <p className="text-xs text-muted-foreground">{finding.detail}</p>
                                      <div className="flex items-center gap-4 mt-2 text-[10px] font-mono text-muted-foreground">
                                        {finding.confidence !== undefined && <span>Confidence: {(finding.confidence * 100).toFixed(0)}%</span>}
                                        {finding.matched_pattern && <span>Pattern: {finding.matched_pattern}</span>}
                                        {finding.offset !== undefined && <span>Offset: {finding.offset}</span>}
                                      </div>
                                    </div>
                                  ))}
                                </div>
                              </div>
                            )}

                            {/* Matched rule */}
                            {ruleId && (
                              <div className="flex items-center gap-2 text-xs">
                                <Shield className="w-3 h-3 text-cordum" />
                                <span className="text-muted-foreground">Matched rule:</span>
                                <span className="font-mono text-cordum">{ruleId}</span>
                              </div>
                            )}
                          </div>
                        </motion.div>
                      )}
                    </AnimatePresence>
                  </div>
                );
              })}
            </div>
          )}
        </>
      )}

      {/* Output Rules Tab */}
      {activeTab === "rules" && (
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <p className="text-xs text-muted-foreground">Rules that govern how output findings are handled</p>
          </div>
          {rulesLoading ? (
            <div className="space-y-3">
              <SkeletonCard />
              <SkeletonCard />
              <SkeletonCard />
            </div>
          ) : rules.length === 0 ? (
            <EmptyState icon={<Shield className="w-8 h-8" />} title="No output rules" description="No output safety rules configured" />
          ) : (
            rules.map((rule) => (
              <div key={rule.id} className="instrument-card p-4 flex items-center gap-4">
                <div className={cn(
                  "w-2 h-2 rounded-full shrink-0",
                  rule.enabled ? "bg-emerald-400" : "bg-gray-500"
                )} />
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 mb-0.5">
                    <span className="font-mono text-sm text-foreground font-medium">{rule.id}</span>
                    <span className={cn(
                      "px-1.5 py-0.5 rounded font-mono text-[10px] font-semibold",
                      rule.decision === "quarantine" ? "bg-amber-400/10 text-amber-400" : rule.decision === "redact" ? "bg-blue-400/10 text-blue-400" : "bg-surface-2 text-muted-foreground"
                    )}>
                      {rule.decision.toUpperCase()}
                    </span>
                  </div>
                  <p className="text-xs text-muted-foreground">{rule.reason ?? rule.description ?? "—"}</p>
                  <div className="flex items-center gap-2 mt-1">
                    {rule.match && Object.entries(rule.match).map(([k, v]) => (
                      <span key={k} className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-surface-2 text-muted-foreground">
                        {k}={String(v)}
                      </span>
                    ))}
                  </div>
                </div>
              </div>
            ))
          )}
        </div>
      )}

      {/* Settings Tab */}
      {activeTab === "settings" && (
        <div className="space-y-4">
          {configLoading ? (
            <SkeletonCard />
          ) : (
            <>
              <div className="instrument-card p-5 space-y-4">
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm font-medium text-foreground">Sensitive Data Detection</p>
                    <p className="text-xs text-muted-foreground">Scan outputs for PII, credentials, and sensitive data</p>
                  </div>
                  <button
                    onClick={handleToggleSensitiveData}
                    disabled={updatePolicyMutation.isPending}
                    className={cn("w-9 h-5 rounded-full relative transition-colors", sensitiveDataEnabled ? "bg-cordum" : "bg-surface-2")}
                  >
                    <div className={cn("absolute top-0.5 w-4 h-4 rounded-full bg-white transition-transform", sensitiveDataEnabled ? "left-[18px]" : "left-0.5")} />
                  </button>
                </div>
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm font-medium text-foreground">Auto-Quarantine</p>
                    <p className="text-xs text-muted-foreground">Automatically quarantine flagged outputs instead of allowing</p>
                  </div>
                  <button
                    onClick={handleToggleAutoQuarantine}
                    disabled={updatePolicyMutation.isPending}
                    className={cn("w-9 h-5 rounded-full relative transition-colors", autoQuarantine ? "bg-cordum" : "bg-surface-2")}
                  >
                    <div className={cn("absolute top-0.5 w-4 h-4 rounded-full bg-white transition-transform", autoQuarantine ? "left-[18px]" : "left-0.5")} />
                  </button>
                </div>
              </div>
            </>
          )}
        </div>
      )}

      {/* Release Confirmation */}
      <ConfirmDialog
        open={!!releaseTarget}
        onClose={() => setReleaseTarget(null)}
        onConfirm={() => {
          if (releaseTarget) releaseMutation.mutate(releaseTarget.id);
          setReleaseTarget(null);
        }}
        title="Release Output"
        description="This will release the quarantined output and deliver it to the requesting agent. Are you sure?"
        confirmLabel="Release"
        variant="default"
      />
    </motion.div>
  );
}
