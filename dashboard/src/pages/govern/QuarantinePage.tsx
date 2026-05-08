/*
 * DESIGN: "Control Surface" — Quarantine Queue
 * Full quarantine management with expandable findings, release/deny actions,
 * severity filter, and search. Uses shared InstrumentCard, MetricValue,
 * StatusBadge, SafetyDecisionBadge, ConfirmDialog, InfoBanner components.
 */
import { useState, useMemo, useCallback } from "react";
import { motion, AnimatePresence } from "framer-motion";
import {
  AlertTriangle,
  ShieldAlert,
  Search,
  RefreshCw,
  ChevronDown,
  ChevronUp,
  CheckCircle2,
  XCircle,
  X,
  FileWarning,
  Gauge,
} from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { MetricValue } from "@/components/ui/MetricValue";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { Input } from "@/components/ui/Input";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import {
  useQuarantinedJobs,
  useReleaseQuarantinedJob,
  useConfirmQuarantine,
  useOutputPolicyStats,
} from "@/hooks/useOutputPolicy";
import { usePolicyAccess } from "@/hooks/usePolicyAccess";
import { cn, formatRelativeTime } from "@/lib/utils";
import type { Job } from "@/api/types";
import type { OutputFinding } from "@/api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function severityVariant(severity: string): "danger" | "warning" | "info" | "muted" {
  switch (severity) {
    case "critical":
      return "danger";
    case "high":
      return "danger";
    case "medium":
      return "warning";
    case "low":
      return "info";
    default:
      return "muted";
  }
}

function severityColor(severity: string): string {
  switch (severity) {
    case "critical":
      return "text-destructive";
    case "high":
      return "text-destructive";
    case "medium":
      return "text-warning";
    case "low":
      return "text-info";
    default:
      return "text-muted-foreground";
  }
}

function getHighestSeverity(findings: OutputFinding[]): string {
  const order = ["critical", "high", "medium", "low", "info"];
  for (const s of order) {
    if (findings.some((f) => f.severity === s)) return s;
  }
  return "unknown";
}

type SeverityFilter = "all" | "critical" | "high" | "medium" | "low";

// ---------------------------------------------------------------------------
// Finding detail row
// ---------------------------------------------------------------------------
function FindingRow({ finding }: { finding: OutputFinding }) {
  return (
    <div className="flex items-start gap-3 py-2">
      <div
        className={cn(
          "mt-0.5 w-2 h-2 rounded-full shrink-0",
          finding.severity === "critical" || finding.severity === "high"
            ? "bg-destructive"
            : finding.severity === "medium"
              ? "bg-warning"
              : "bg-info",
        )}
      />
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 mb-0.5">
          <span className={cn("text-xs font-mono font-semibold", severityColor(finding.severity))}>
            {finding.severity.toUpperCase()}
          </span>
          <span className="text-xs font-mono text-muted-foreground">{finding.type}</span>
          {finding.scanner && (
            <span className="text-xs px-1.5 py-0.5 rounded bg-surface-2 text-muted-foreground font-mono">
              {finding.scanner}
            </span>
          )}
          {finding.confidence != null && (
            <span className="text-xs text-muted-foreground font-mono">
              {Math.round(finding.confidence * 100)}% conf
            </span>
          )}
        </div>
        <p className="text-xs text-foreground/90 leading-relaxed">{finding.detail}</p>
        {finding.matched_pattern && (
          <p className="mt-1 text-xs font-mono text-muted-foreground">
            Pattern: <code className="px-1 py-0.5 rounded bg-surface-2 text-foreground/80">{finding.matched_pattern}</code>
          </p>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Quarantine item card
// ---------------------------------------------------------------------------
function QuarantineItemCard({
  item,
  expanded,
  onToggle,
  canRelease,
  onRelease,
  onDeny,
}: {
  item: Job;
  expanded: boolean;
  onToggle: () => void;
  canRelease: boolean;
  onRelease: () => void;
  onDeny: () => void;
}) {
  const findings = item.output_safety?.findings ?? [];
  const highestSeverity = getHighestSeverity(findings);
  const criticalCount = findings.filter(
    (f) => f.severity === "critical" || f.severity === "high",
  ).length;

  return (
    <motion.div
      layout
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, x: -100, height: 0, overflow: "hidden" }}
      transition={{ duration: 0.25 }}
      className={cn(
        "instrument-card overflow-hidden transition-colors",
        highestSeverity === "critical" && "border-destructive/30",
        highestSeverity === "high" && "border-destructive/20",
        highestSeverity === "medium" && "border-warning/20",
      )}
    >
      {/* Header row */}
      <div
        className="flex items-start justify-between gap-4 cursor-pointer"
        onClick={onToggle}
      >
        <div className="flex-1 min-w-0">
          <div className="flex flex-wrap items-center gap-2 mb-1.5">
            <span className="font-mono text-sm text-cordum">{item.id.slice(0, 16)}</span>
            <SafetyDecisionBadge decision={item.output_safety?.decision} />
            <StatusBadge variant={severityVariant(highestSeverity)}>
              {highestSeverity}
            </StatusBadge>
            {criticalCount > 0 && (
              <span className="text-xs font-mono px-1.5 py-0.5 rounded bg-destructive/10 text-destructive">
                {criticalCount} critical finding{criticalCount > 1 ? "s" : ""}
              </span>
            )}
            <span className="text-xs text-muted-foreground font-mono">
              {formatRelativeTime(item.updatedAt)}
            </span>
          </div>
          <p className="text-xs text-muted-foreground leading-relaxed">
            {item.output_safety?.reason?.trim() || "Output quarantined by policy scanners"}
          </p>
          {item.topic && (
            <p className="mt-1 text-xs font-mono text-muted-foreground">
              topic: <span className="text-foreground/80">{item.topic}</span>
            </p>
          )}
        </div>
        <div className="flex items-center gap-2 shrink-0">
          {canRelease && (
            <div className="flex gap-1.5">
              <Button
                size="sm"
                variant="danger"
                onClick={(e) => {
                  e.stopPropagation();
                  onDeny();
                }}
              >
                <XCircle className="w-3.5 h-3.5 mr-1" />
                Deny
              </Button>
              <Button
                size="sm"
                variant="primary"
                onClick={(e) => {
                  e.stopPropagation();
                  onRelease();
                }}
              >
                <CheckCircle2 className="w-3.5 h-3.5 mr-1" />
                Release
              </Button>
            </div>
          )}
          <button type="button" className="p-1 rounded hover:bg-surface-2 transition-colors text-muted-foreground">
            {expanded ? <ChevronUp className="w-4 h-4" /> : <ChevronDown className="w-4 h-4" />}
          </button>
        </div>
      </div>

      {/* Expanded detail */}
      <AnimatePresence>
        {expanded && (
          <motion.div
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.2 }}
            className="overflow-hidden"
          >
            <div className="mt-4 pt-4 border-t border-border">
              {/* Findings */}
              {findings.length > 0 ? (
                <div className="space-y-1">
                  <p className="text-xs font-mono uppercase tracking-wider text-muted-foreground mb-2">
                    {findings.length} Finding{findings.length > 1 ? "s" : ""}
                  </p>
                  <div className="divide-y divide-border/50">
                    {findings.map((f, i) => (
                      <FindingRow key={i} finding={f} />
                    ))}
                  </div>
                </div>
              ) : (
                <p className="text-xs text-muted-foreground">No detailed findings available.</p>
              )}

              {/* Metadata row */}
              <div className="mt-4 flex flex-wrap gap-4 text-xs font-mono text-muted-foreground">
                {item.output_safety?.rule_id && (
                  <span>
                    Rule: <span className="text-foreground/80">{item.output_safety.rule_id}</span>
                  </span>
                )}
                {item.output_safety?.phase && (
                  <span>
                    Phase: <span className="text-foreground/80">{item.output_safety.phase}</span>
                  </span>
                )}
                {item.output_safety?.policy_snapshot && (
                  <span>
                    Snapshot: <span className="text-foreground/80">{item.output_safety.policy_snapshot.slice(0, 12)}</span>
                  </span>
                )}
              </div>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </motion.div>
  );
}

// ---------------------------------------------------------------------------
// Main page
// ---------------------------------------------------------------------------
export default function QuarantinePage() {
  const policyAccess = usePolicyAccess();
  const canRelease = policyAccess.canRelease;
  const { data, isLoading, isError, error, refetch } = useQuarantinedJobs();
  const { data: stats } = useOutputPolicyStats();
  const releaseMut = useReleaseQuarantinedJob();
  const confirmMut = useConfirmQuarantine();

  const [search, setSearch] = useState("");
  const [severityFilter, setSeverityFilter] = useState<SeverityFilter>("all");
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [releaseTarget, setReleaseTarget] = useState<Job | null>(null);
  const [denyTarget, setDenyTarget] = useState<Job | null>(null);

  const items = useMemo(() => data?.items ?? [], [data]);

  const highSeverityCount = useMemo(
    () =>
      items.filter((item) =>
        (item.output_safety?.findings ?? []).some(
          (f) => f.severity === "critical" || f.severity === "high",
        ),
      ).length,
    [items],
  );

  const filtered = useMemo(() => {
    return items.filter((item) => {
      // severity filter
      if (severityFilter !== "all") {
        const findings = item.output_safety?.findings ?? [];
        const hasSeverity = findings.some((f) => f.severity === severityFilter);
        if (!hasSeverity) return false;
      }
      // search
      if (search) {
        const q = search.toLowerCase();
        return (
          item.id.toLowerCase().includes(q) ||
          (item.topic ?? "").toLowerCase().includes(q) ||
          (item.output_safety?.reason ?? "").toLowerCase().includes(q) ||
          (item.output_safety?.findings ?? []).some(
            (f) => f.type.toLowerCase().includes(q) || f.detail.toLowerCase().includes(q),
          )
        );
      }
      return true;
    });
  }, [items, severityFilter, search]);

  const handleRelease = useCallback(
    (item: Job) => {
      releaseMut.mutate(item.id, {
        onSuccess: () => {
          setReleaseTarget(null);
          void refetch();
        },
      });
    },
    [releaseMut, refetch],
  );

  const handleDeny = useCallback(
    (item: Job) => {
      confirmMut.mutate(item.id, {
        onSuccess: () => {
          setDenyTarget(null);
          void refetch();
        },
      });
    },
    [confirmMut, refetch],
  );

  return (
    <div className="space-y-6">
      <PageHeader
        label="Govern"
        title="Quarantine Queue"
        subtitle="Review quarantined outputs flagged by policy scanners. Inspect findings, release safe items, or deny violations."
        actions={
          <div className="flex items-center gap-2">
            <StatusBadge variant={canRelease ? "healthy" : "muted"}>
              {canRelease ? "release access" : "read-only"}
            </StatusBadge>
            <Button variant="outline" size="sm" onClick={() => void refetch()}>
              <RefreshCw className="w-3 h-3 mr-1" />
              Refresh
            </Button>
          </div>
        }
      />

      {/* KPI cards */}
      {isLoading ? (
        <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
          <SkeletonCard />
          <SkeletonCard />
          <SkeletonCard />
          <SkeletonCard />
        </div>
      ) : (
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3 }}
          className="grid grid-cols-2 md:grid-cols-4 gap-4"
        >
          <InstrumentCard accent={items.length > 0 ? "warning" : "muted"}>
            <MetricValue
              label="Queue size"
              value={items.length}
              icon={<ShieldAlert className={cn("w-4 h-4", items.length > 0 ? "text-warning" : "text-muted-foreground")} />}
            />
          </InstrumentCard>
          <InstrumentCard accent={highSeverityCount > 0 ? "danger" : "muted"}>
            <MetricValue
              label="High severity"
              value={highSeverityCount}
              icon={<AlertTriangle className={cn("w-4 h-4", highSeverityCount > 0 ? "text-destructive" : "text-muted-foreground")} />}
            />
          </InstrumentCard>
          <InstrumentCard accent="info">
            <MetricValue
              label="Checks (24h)"
              value={stats?.totalChecks24h ?? 0}
              icon={<Gauge className="w-4 h-4 text-info" />}
            />
          </InstrumentCard>
          <InstrumentCard accent="cordum">
            <MetricValue
              label="Quarantined (24h)"
              value={stats?.quarantined24h ?? 0}
              icon={<FileWarning className="w-4 h-4 text-cordum" />}
            />
          </InstrumentCard>
        </motion.div>
      )}

      {/* Error state */}
      {isError && (
        <EmptyState
          icon={<AlertTriangle className="w-6 h-6" />}
          title="Unable to load quarantine queue"
          description={
            error instanceof Error
              ? error.message
              : "An unexpected error occurred while loading quarantine data."
          }
          action={
            <Button variant="outline" size="sm" onClick={() => void refetch()}>
              Retry
            </Button>
          }
        />
      )}

      {!isLoading && !isError && (
        <>
          {/* Filters */}
          <div className="flex items-center gap-3 flex-wrap">
            <div className="relative flex-1 max-w-sm">
              <Input
                type="text"
                icon={<Search className="w-3.5 h-3.5" />}
                placeholder="Search by ID, topic, finding..."
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                className="h-8 text-xs"
                aria-label="Search quarantined items"
              />
            </div>
            <div className="flex items-center gap-1 bg-surface-1 border border-border rounded-2xl p-0.5">
              {(
                [
                  { id: "all", label: "All" },
                  { id: "critical", label: "Critical" },
                  { id: "high", label: "High" },
                  { id: "medium", label: "Medium" },
                  { id: "low", label: "Low" },
                ] as { id: SeverityFilter; label: string }[]
              ).map((tab) => (
                <button type="button"
                  key={tab.id}
                  onClick={() => setSeverityFilter(tab.id)}
                  className={cn(
                    "px-3 py-1.5 text-xs font-medium rounded transition-colors",
                    severityFilter === tab.id
                      ? "bg-cordum/10 text-cordum"
                      : "text-muted-foreground hover:text-foreground",
                  )}
                >
                  {tab.label}
                </button>
              ))}
            </div>
            {(search || severityFilter !== "all") && (
              <button type="button"
                onClick={() => {
                  setSearch("");
                  setSeverityFilter("all");
                }}
                className="flex items-center gap-1 px-2 py-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
              >
                <X className="w-3 h-3" />
                Clear
              </button>
            )}
          </div>

          {/* Info banner */}
          {!canRelease && items.length > 0 && (
            <InfoBanner variant="warning">
              Viewer mode: quarantine items are read-only. Contact an admin for release access.
            </InfoBanner>
          )}

          {/* Items list */}
          {filtered.length === 0 ? (
            <EmptyState
              icon={<ShieldAlert className="w-5 h-5" />}
              title={items.length === 0 ? "No quarantined outputs" : "No matching items"}
              description={
                items.length === 0
                  ? "All clear — no output items are currently quarantined."
                  : "Try adjusting your search or severity filter."
              }
            />
          ) : (
            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider">
                  {filtered.length} of {items.length} item{items.length !== 1 ? "s" : ""}
                </p>
              </div>
              <AnimatePresence mode="popLayout">
                {filtered.map((item) => (
                  <QuarantineItemCard
                    key={item.id}
                    item={item}
                    expanded={expandedId === item.id}
                    onToggle={() =>
                      setExpandedId((prev) => (prev === item.id ? null : item.id))
                    }
                    canRelease={canRelease}
                    onRelease={() => setReleaseTarget(item)}
                    onDeny={() => setDenyTarget(item)}
                  />
                ))}
              </AnimatePresence>
            </div>
          )}
        </>
      )}

      {/* Release confirmation */}
      <ConfirmDialog
        open={!!releaseTarget}
        onClose={() => setReleaseTarget(null)}
        onConfirm={() => releaseTarget && handleRelease(releaseTarget)}
        title="Release Quarantined Output"
        description={`Release output ${releaseTarget?.id.slice(0, 16)}? This will retry the job with its original output.`}
        confirmLabel="Release"
        loading={releaseMut.isPending}
      />

      {/* Deny confirmation */}
      <ConfirmDialog
        open={!!denyTarget}
        onClose={() => setDenyTarget(null)}
        onConfirm={() => denyTarget && handleDeny(denyTarget)}
        title="Deny Quarantined Output"
        description={`Permanently deny output ${denyTarget?.id.slice(0, 16)}? The job will be marked as failed.`}
        confirmLabel="Deny"
        variant="destructive"
        loading={confirmMut.isPending}
      />
    </div>
  );
}
