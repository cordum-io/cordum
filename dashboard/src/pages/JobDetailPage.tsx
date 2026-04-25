/*
 * DESIGN: "Intelligence Dossier" — Job Detail
 * Narrative-driven: what the agent tried → what the platform decided → why.
 * Story in 5 seconds. Bloomberg terminal precision, Apple Keynote clarity.
 */
import { useParams, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
import { get } from "@/api/client";
import { mapJobDetail, type BackendJobDetail } from "@/api/transform";
import type { Job, OutputFinding } from "@/api/types";
import {
  StatusBadge,
  statusToneBorderTextClasses,
  statusToneDotClasses,
  statusToneGradientClasses,
  statusToneTextClasses,
  type BadgeVariant,
} from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  ArrowLeft, Copy, Check, Clock, Shield, ShieldCheck, ShieldX, ShieldAlert,
  AlertTriangle, Eye, Tag, Zap, CheckCircle2, XCircle, Timer,
  Store, Package, Users, Building2, CreditCard, ChevronRight, Workflow, MessageSquare, ArrowRight,
} from "lucide-react";
import { cn, formatRelativeTime, formatDuration } from "@/lib/utils";
import { getJobParentRefs } from "@/lib/jobParentRefs";
import { useElapsedTimer } from "@/hooks/useElapsedTimer";
import { useState, useMemo, useCallback } from "react";
import { toast } from "sonner";
import { useEventStore } from "@/state/events";
import { JobActions } from "@/components/jobs/JobActions";
import { CollapsibleSection } from "@/components/ui/CollapsibleSection";
import { CodeBlock } from "@/components/ui/CodeBlock";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Tabs } from "@/components/ui/Tabs";
import { GovernanceTimeline } from "@/components/governance/GovernanceTimeline";

// ---------------------------------------------------------------------------
// Status configuration — colors, icons, labels for each outcome
// ---------------------------------------------------------------------------

interface StatusConfig {
  icon: typeof CheckCircle2;
  label: string;
  badgeVariant: BadgeVariant;
  tone: BadgeVariant;
}

// Using string keys so we can also handle backend aliases like "completed"/"timed_out"
const STATUS_CONFIGS: { [key: string]: StatusConfig } = {
  succeeded: {
    icon: CheckCircle2,
    label: "Succeeded",
    badgeVariant: "cordum",
    tone: "healthy",
  },
  completed: {
    icon: CheckCircle2,
    label: "Succeeded",
    badgeVariant: "cordum",
    tone: "healthy",
  },
  denied: {
    icon: ShieldX,
    label: "Denied",
    badgeVariant: "governance",
    tone: "governance",
  },
  output_quarantined: {
    icon: ShieldAlert,
    label: "Output Quarantined",
    badgeVariant: "danger",
    tone: "danger",
  },
  failed: {
    icon: XCircle,
    label: "Failed",
    badgeVariant: "danger",
    tone: "danger",
  },
  timeout: {
    icon: Timer,
    label: "Timed Out",
    badgeVariant: "danger",
    tone: "danger",
  },
  timed_out: {
    icon: Timer,
    label: "Timed Out",
    badgeVariant: "danger",
    tone: "danger",
  },
  running: {
    icon: Zap,
    label: "Running",
    badgeVariant: "info",
    tone: "info",
  },
  dispatched: {
    icon: Zap,
    label: "Dispatched",
    badgeVariant: "info",
    tone: "info",
  },
  approval_required: {
    icon: ShieldAlert,
    label: "Awaiting Approval",
    badgeVariant: "warning",
    tone: "warning",
  },
  pending: {
    icon: Clock,
    label: "Pending",
    badgeVariant: "warning",
    tone: "warning",
  },
  scheduled: {
    icon: Clock,
    label: "Scheduled",
    badgeVariant: "warning",
    tone: "warning",
  },
  cancelled: {
    icon: XCircle,
    label: "Cancelled",
    badgeVariant: "muted",
    tone: "muted",
  },
};

function getStatusConfig(status: string): StatusConfig {
  return STATUS_CONFIGS[status] ?? {
    icon: Clock,
    label: status,
    badgeVariant: "muted" as const,
    tone: "muted",
  };
}

// ---------------------------------------------------------------------------
// Smart summary generator — one-line story from job context
// ---------------------------------------------------------------------------

function generateJobSummary(job: Job): string {
  const ctx = job.context as Record<string, unknown> | undefined;
  if (!ctx) return job.topic.replace("job.", "").replace(/\./g, " \u2192 ");

  const merchant = ctx.merchant as Record<string, unknown> | undefined;
  const items = ctx.items as Array<Record<string, unknown>> | undefined;
  const total = ctx.total as number | undefined;
  const currency = (ctx.currency as string) ?? "USD";

  if (merchant && total != null) {
    const itemName = items?.[0]?.name ?? "items";
    const sym = currency === "ILS" ? "\u20AA" : currency === "EUR" ? "\u20AC" : currency === "GBP" ? "\u00A3" : "$";
    return `${sym}${Number(total).toLocaleString()} to ${merchant.name} for ${itemName}`;
  }

  const company = ctx.company as string | undefined;
  const employeeCount = ctx.employee_count as number | undefined;
  if (company && employeeCount) return `${company} onboarding \u2014 ${employeeCount} employees`;
  if (company) return `${company} \u2014 ${ctx.department ?? "corporate"}`;

  if (ctx.instruction) return String(ctx.instruction);
  if (ctx.prompt) return String(ctx.prompt);
  if (ctx.query) return String(ctx.query);
  if (ctx.trigger) return String(ctx.trigger);
  if (ctx.command) return String(ctx.command);
  if (ctx.message) return String(ctx.message);

  return job.topic.replace("job.", "").replace(/\./g, " \u2192 ");
}

// ---------------------------------------------------------------------------
// Hero Banner — the 5-second verdict
// ---------------------------------------------------------------------------

function HeroBanner({ job, elapsed, isActive }: { job: Job; elapsed: string; isActive: boolean }) {
  const config = getStatusConfig(job.status);
  const Icon = config.icon;
  const summary = generateJobSummary(job);
  const rule = job.safetyDecision?.matchedRule;
  const [copied, setCopied] = useState(false);

  const handleCopyJobId = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(job.id);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      toast.error("Copy failed");
    }
  }, [job.id]);

  return (
    <motion.div
      initial={{ opacity: 0, y: -8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.4, ease: [0.16, 1, 0.3, 1] }}
        className={cn(
          "relative overflow-hidden rounded-2xl border border-border",
          "bg-gradient-to-r",
          statusToneGradientClasses[config.tone],
          "p-6 md:p-8",
        )}
    >
      <div
        className="absolute inset-0 opacity-[0.03] pointer-events-none"
        style={{
          backgroundImage: "radial-gradient(circle at 1px 1px, currentColor 1px, transparent 0)",
          backgroundSize: "24px 24px",
        }}
      />

      <div className="relative flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-3 mb-3">
            <div className={cn("p-2 rounded-xl bg-card border border-border", statusToneTextClasses[config.tone])}>
              <Icon className="w-5 h-5" />
            </div>
            <StatusBadge
              variant={config.badgeVariant}
              dot
              pulse={isActive}
              className="text-sm font-semibold px-3 py-1"
            >
              {config.label}
            </StatusBadge>
            {isActive && (
              <span className="text-xs font-mono text-muted-foreground flex items-center gap-1.5">
                <span className={cn("w-1.5 h-1.5 rounded-full animate-pulse", statusToneDotClasses[config.tone])} />
                {elapsed}
              </span>
            )}
            {!isActive && job.createdAt && job.updatedAt && (
              <span className="text-xs font-mono text-muted-foreground">
                {formatDuration(new Date(job.updatedAt).getTime() - new Date(job.createdAt).getTime())}
              </span>
            )}
          </div>

          <h1 className="font-display text-xl md:text-2xl font-bold text-foreground tracking-tight leading-snug mb-2">
            {summary}
          </h1>

          {rule && (
            <div className="flex items-center gap-2 mt-1">
              <Shield className="w-3.5 h-3.5 text-muted-foreground" />
              <span className="text-sm font-mono text-muted-foreground">{rule}</span>
            </div>
          )}

          {job.safetyDecision?.reason && job.safetyDecision.type !== "allow" && (
            <p className="text-sm text-muted-foreground mt-1 max-w-2xl">{job.safetyDecision.reason}</p>
          )}
        </div>

        <div className="hidden md:flex flex-col items-end gap-1 shrink-0">
          <span className="text-xs font-mono text-muted-foreground" title={job.createdAt}>
            {formatRelativeTime(job.createdAt)}
          </span>
          <div className="flex items-center gap-1.5">
            <span className="text-[10px] font-mono text-muted-foreground/60 tracking-tight">
              {job.id.slice(0, 12)}\u2026
            </span>
            <Button
              onClick={handleCopyJobId}
              variant="ghost"
              size="icon"
              aria-label="Copy job ID"
              className="h-7 w-7"
            >
              {copied ? <Check className="w-3 h-3" /> : <Copy className="w-3 h-3" />}
            </Button>
          </div>
        </div>
      </div>
    </motion.div>
  );
}

export function ParentContextBanner({ job }: { job: Job }) {
  const navigate = useNavigate();
  const { runId, sessionId, workflowId } = getJobParentRefs(job);
  const untrustedPrompt = (job.metadata?.untrusted_prompt_text as string) || (job.labels?.untrusted_prompt_text as string);

  if (!runId && !sessionId) return null;

  return (
    <motion.div
      initial={{ opacity: 0, y: -8 }}
      animate={{ opacity: 1, y: 0 }}
      className="instrument-card border-dashed border-cordum/30 bg-cordum/5 mb-6"
    >
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div className="flex items-start gap-3">
          <div className="mt-1 p-1.5 rounded-lg bg-cordum/10 text-cordum">
            {runId ? <Workflow className="w-4 h-4" /> : <MessageSquare className="w-4 h-4" />}
          </div>
          <div className="min-w-0">
            <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest mb-1">
              Part of {runId ? "Workflow Run" : "Copilot Session"}
            </p>
            <div className="flex items-center gap-2">
              <span className="text-sm font-semibold text-foreground truncate">
                {runId ? `Run: ${runId.slice(0, 12)}...` : `Session: ${sessionId?.slice(0, 12) ?? ""}...`}
              </span>
              {untrustedPrompt && (
                <span className="text-xs text-muted-foreground border-l border-border pl-2 italic truncate max-w-md hidden sm:inline">
                  &ldquo;{untrustedPrompt}&rdquo;
                </span>
              )}
            </div>
          </div>
        </div>
        <Button
          variant="outline"
          size="sm"
          disabled={!(runId && workflowId) && !sessionId}
          onClick={() => {
            if (runId && workflowId) {
              navigate(`/workflows/${workflowId}/runs/${runId}`);
            } else if (sessionId) {
              navigate(`/copilot/sessions/${sessionId}`);
            }
          }}
          className="shrink-0"
        >
          View Parent <ArrowRight className="w-3.5 h-3.5 ml-1.5" />
        </Button>
      </div>
    </motion.div>
  );
}

// ---------------------------------------------------------------------------
// Smart Context — auto-detects job type and renders tailored view
// ---------------------------------------------------------------------------

function SmartContext({ job }: { job: Job }) {
  const ctx = (job.context ?? {}) as Record<string, unknown>;
  const { runId, sessionId } = getJobParentRefs(job);

  const merchant = ctx.merchant as Record<string, unknown> | undefined;
  const total = ctx.total as number | undefined;
  const isPayment = !!(merchant && total != null);

  const company = ctx.company as string | undefined;
  const isB2B = !!(company && (ctx.employee_count != null || ctx.department));

  return (
    <div className="space-y-6">
      <ParentContextBanner job={job} />

      {isPayment && <PaymentContext ctx={ctx} />}
      {isB2B && <B2BContext ctx={ctx} />}
      {Object.keys(ctx).length > 0 && !isPayment && !isB2B && <GenericContext ctx={ctx} />}

      {Object.keys(ctx).length === 0 && !runId && !sessionId && (
        <div className="instrument-card p-8 flex flex-col items-center justify-center text-center opacity-50">
          <Zap className="w-8 h-8 mb-2 text-muted-foreground" />
          <p className="text-sm text-muted-foreground italic">No extended context available for this job.</p>
        </div>
      )}
    </div>
  );
}

function PaymentContext({ ctx }: { ctx: Record<string, unknown> }) {
  const merchant = ctx.merchant as Record<string, unknown>;
  const items = ctx.items as Array<Record<string, unknown>> | undefined;
  const total = ctx.total as number;
  const currency = (ctx.currency as string) ?? "USD";
  const sym = currency === "ILS" ? "\u20AA" : currency === "EUR" ? "\u20AC" : currency === "GBP" ? "\u00A3" : "$";
  const agent = ctx.agent as Record<string, unknown> | undefined;

  return (
    <motion.div
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3, delay: 0.05 }}
      className="instrument-card"
    >
      <div className="flex items-center gap-2 mb-4">
        <CreditCard className="w-4 h-4 text-cordum" />
        <h2 className="font-display font-semibold text-sm text-foreground">Payment Details</h2>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div className="space-y-1">
          <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Merchant</p>
          <div className="flex items-center gap-2">
            <Store className="w-4 h-4 text-muted-foreground" />
            <div>
              <p className="text-sm font-semibold text-foreground">{String(merchant.name)}</p>
              {!!merchant.domain && <p className="text-xs text-muted-foreground">{String(merchant.domain)}</p>}
            </div>
          </div>
          {!!merchant.mcc && (
            <p className="text-xs font-mono text-muted-foreground">MCC: {String(merchant.mcc)}</p>
          )}
        </div>

        <div className="space-y-1">
          <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Total</p>
          <p className="text-2xl font-display font-bold text-foreground tracking-tight">
            {sym}{Number(total).toLocaleString()}
          </p>
          <p className="text-xs text-muted-foreground">{currency}</p>
        </div>

        {agent && (
          <div className="space-y-1">
            <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Agent</p>
            <p className="text-sm font-mono text-foreground">{String(agent.id)}</p>
            {!!agent.tap_verified && <StatusBadge variant="cordum">TAP Verified</StatusBadge>}
          </div>
        )}
      </div>

      {items && items.length > 0 && (
        <div className="mt-4 pt-4 border-t border-border">
          <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider mb-2">Items</p>
          <div className="space-y-1.5">
            {items.map((item, i) => (
              <div key={i} className="flex items-center justify-between text-sm">
                <div className="flex items-center gap-2 min-w-0">
                  <Package className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
                  <span className="text-foreground truncate">{String(item.name)}</span>
                  {!!item.category && (
                    <span className="text-xs px-1.5 py-0.5 rounded bg-surface-2 text-muted-foreground font-mono shrink-0">
                      {String(item.category)}
                    </span>
                  )}
                </div>
                {item.price != null && (
                  <span className="font-mono text-foreground shrink-0 ml-3">
                    {sym}{Number(item.price).toLocaleString()}
                  </span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {!!ctx.instruction && (
        <div className="mt-4 pt-4 border-t border-border">
          <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider mb-1">Instruction</p>
          <p className="text-sm text-foreground italic">&ldquo;{String(ctx.instruction)}&rdquo;</p>
        </div>
      )}
    </motion.div>
  );
}

function B2BContext({ ctx }: { ctx: Record<string, unknown> }) {
  const items = ctx.items as Array<Record<string, unknown>> | undefined;
  const currency = (ctx.currency as string) ?? "USD";
  const sym = currency === "ILS" ? "\u20AA" : currency === "EUR" ? "\u20AC" : currency === "GBP" ? "\u00A3" : "$";

  return (
    <motion.div
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3, delay: 0.05 }}
      className="instrument-card"
    >
      <div className="flex items-center gap-2 mb-4">
        <Building2 className="w-4 h-4 text-cordum" />
        <h2 className="font-display font-semibold text-sm text-foreground">Procurement Details</h2>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        {!!ctx.company && (
          <div>
            <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Company</p>
            <p className="text-sm font-semibold text-foreground mt-1">{String(ctx.company)}</p>
          </div>
        )}
        {!!ctx.department && (
          <div>
            <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Department</p>
            <p className="text-sm text-foreground mt-1">{String(ctx.department)}</p>
          </div>
        )}
        {ctx.employee_count != null && (
          <div>
            <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Employees</p>
            <div className="flex items-center gap-1.5 mt-1">
              <Users className="w-3.5 h-3.5 text-muted-foreground" />
              <p className="text-sm font-semibold text-foreground">{String(ctx.employee_count)}</p>
            </div>
          </div>
        )}
        {ctx.total != null && (
          <div>
            <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Total</p>
            <p className="text-xl font-display font-bold text-foreground mt-1">
              {sym}{Number(ctx.total).toLocaleString()}
            </p>
          </div>
        )}
      </div>

      {!!(ctx.trigger || ctx.instruction) && (
        <div className="mt-4 pt-4 border-t border-border">
          <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider mb-1">Trigger</p>
          <p className="text-sm text-foreground">{String(ctx.trigger ?? ctx.instruction)}</p>
        </div>
      )}

      {items && items.length > 0 && (
        <div className="mt-4 pt-4 border-t border-border">
          <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider mb-2">Items</p>
          <div className="space-y-1.5">
            {items.map((item, i) => (
              <div key={i} className="flex items-center justify-between text-sm">
                <span className="text-foreground">{String(item.name)}</span>
                {item.price != null && (
                  <span className="font-mono text-foreground">{sym}{Number(item.price).toLocaleString()}</span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}
    </motion.div>
  );
}

function GenericContext({ ctx }: { ctx: Record<string, unknown> }) {
  const skip = new Set(["type", "session_id", "run_id", "consumer_token", "untrusted_prompt_text"]);
  const priority = ["instruction", "prompt", "query", "command", "trigger", "message"];
  const entries = Object.entries(ctx).filter(([k]) => !skip.has(k));
  if (entries.length === 0) return null;

  entries.sort(([a], [b]) => {
    const ai = priority.indexOf(a);
    const bi = priority.indexOf(b);
    if (ai !== -1 && bi !== -1) return ai - bi;
    if (ai !== -1) return -1;
    if (bi !== -1) return 1;
    return a.localeCompare(b);
  });

  const formatValue = (v: unknown): string => {
    if (v == null) return "";
    if (typeof v === "string" || typeof v === "number") return String(v);
    if (typeof v === "boolean") return v ? "Yes" : "No";
    if (Array.isArray(v)) {
      return v.map((item) => {
        if (typeof item === "object" && item !== null) {
          const r = item as Record<string, unknown>;
          const name = r.name ?? r.title ?? r.id ?? "";
          const extra = r.price != null ? ` ($${r.price})` : r.status ? ` [${r.status}]` : "";
          return name ? `${name}${extra}` : JSON.stringify(r);
        }
        return String(item);
      }).join(", ");
    }
    if (typeof v === "object") {
      const r = v as Record<string, unknown>;
      const name = r.name ?? r.title ?? r.id ?? "";
      const domain = r.domain ?? r.url ?? r.host ?? "";
      if (name && domain) return `${name} (${domain})`;
      if (name) return String(name);
      return Object.entries(r).map(([k2, v2]) => `${k2}: ${v2}`).join(", ");
    }
    return String(v);
  };

  const formatLabel = (key: string): string =>
    key.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());

  return (
    <motion.div
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3, delay: 0.05 }}
      className="instrument-card"
    >
      <div className="flex items-center gap-2 mb-4">
        <Tag className="w-4 h-4 text-cordum" />
        <h2 className="font-display font-semibold text-sm text-foreground">Job Context</h2>
      </div>
      <dl className="grid grid-cols-[120px_1fr] gap-x-6 gap-y-3 items-baseline">
        {entries.map(([key, value]) => {
          const formatted = formatValue(value);
          if (!formatted) return null;
          return (
            <div key={key} className="contents">
              <dt className="text-xs font-mono text-muted-foreground uppercase tracking-wider">{formatLabel(key)}</dt>
              <dd className="text-sm text-foreground break-words">{formatted}</dd>
            </div>
          );
        })}
      </dl>
    </motion.div>
  );
}

// ---------------------------------------------------------------------------
// Safety Timeline — vertical narrative of the safety story
// ---------------------------------------------------------------------------

interface SafetyStep {
  icon: typeof Shield;
  label: string;
  decision?: string;
  rule?: string;
  detail?: string;
  evalTime?: string;
  variant: BadgeVariant;
  findings?: OutputFinding[];
  evalPath?: string[];
  redacted?: { ptr?: string; data?: unknown };
}

function SafetyTimeline({ job }: { job: Job }) {
  const steps: SafetyStep[] = [];

  if (job.safetyDecision) {
    const d = job.safetyDecision;
    const variant: BadgeVariant = d.type === "allow" || d.type === "allow_with_constraints" ? "healthy"
      : d.type === "deny" ? "governance"
      : d.type === "require_approval" ? "warning"
      : "muted";
    steps.push({
      icon: ShieldCheck,
      label: "Input Policy",
      decision: d.type.replace(/_/g, " "),
      rule: d.matchedRule,
      detail: d.reason,
      evalTime: d.evalTimeMs ? `${d.evalTimeMs}ms` : undefined,
      variant,
      evalPath: d.evalPath,
    });
  }

  if (job.safetyDecision?.type !== "deny") {
    const s = job.status as string;
    const workerVariant: BadgeVariant =
      ["running", "dispatched"].includes(s) ? "info"
      : ["succeeded", "completed"].includes(s) || job.output_safety ? "healthy"
      : ["failed", "timeout", "timed_out"].includes(s) ? "danger"
      : "muted";
    const workerDecision =
      ["running", "dispatched", "pending", "scheduled"].includes(s) ? "in progress"
      : ["succeeded", "completed"].includes(s) ? "completed"
      : s;
    steps.push({
      icon: Zap,
      label: "Worker Execution",
      decision: workerDecision,
      detail: job.topic,
      variant: workerVariant,
    });
  }

  if (job.output_safety) {
    const os = job.output_safety;
    const variant: BadgeVariant = os.decision === "ALLOW" ? "healthy"
      : os.decision === "REDACT" ? "warning"
      : "danger";
    steps.push({
      icon: ShieldAlert,
      label: "Output Policy",
      decision: os.decision,
      rule: os.rule_id,
      detail: os.reason,
      variant,
      findings: os.findings,
      redacted: os.decision === "REDACT" ? { ptr: os.redacted_ptr, data: os.redacted } : undefined,
    });
  }

  if (steps.length === 0) {
    return <p className="text-sm text-muted-foreground italic">No safety evaluation recorded.</p>;
  }

  return (
    <div className="relative">
      {steps.map((step, i) => {
        const Icon = step.icon;
        const isLast = i === steps.length - 1;

        return (
          <div key={step.label} className="relative flex gap-4">
            {/* Vertical spine */}
            <div className="flex flex-col items-center">
              <div className={cn(
                "w-8 h-8 rounded-full flex items-center justify-center border-2 bg-card shrink-0",
                statusToneBorderTextClasses[step.variant],
              )}>
                <Icon className="w-4 h-4" />
              </div>
              {!isLast && <div className="w-px flex-1 min-h-[24px] bg-border my-1" />}
            </div>

            {/* Content */}
            <div className={cn("pb-6 min-w-0 flex-1", isLast && "pb-0")}>
              <div className="flex items-center gap-2 flex-wrap">
                <span className="text-sm font-semibold text-foreground">{step.label}</span>
                {step.decision && <StatusBadge variant={step.variant}>{step.decision}</StatusBadge>}
                {step.evalTime && <span className="text-xs font-mono text-muted-foreground">{step.evalTime}</span>}
              </div>
              {step.rule && <p className="text-xs font-mono text-muted-foreground mt-1">{step.rule}</p>}
              {step.detail && <p className="text-xs text-muted-foreground mt-0.5 leading-relaxed">{step.detail}</p>}

              {step.evalPath && step.evalPath.length > 0 && (
                <div className="flex items-center gap-1 flex-wrap mt-2">
                  {step.evalPath.map((p, pi) => (
                    <span key={p} className="inline-flex items-center">
                      <span className="px-1.5 py-0.5 rounded bg-surface-2 border border-border text-[10px] font-mono text-foreground">{p}</span>
                      {pi < (step.evalPath?.length ?? 0) - 1 && (
                        <ChevronRight className="w-3 h-3 text-muted-foreground mx-0.5" />
                      )}
                    </span>
                  ))}
                </div>
              )}

              {step.findings && step.findings.length > 0 && (
                <div className="mt-2 space-y-1.5">
                  {step.findings.map((f) => (
                    <div key={`${f.type}-${f.scanner ?? ""}-${f.detail.slice(0, 40)}`} className="surface-inset p-2.5 rounded-xl">
                      <div className="flex items-center gap-2 mb-0.5">
                        <StatusBadge variant={f.severity === "critical" ? "danger" : f.severity === "high" ? "warning" : "muted"}>{f.severity}</StatusBadge>
                        <span className="text-xs font-mono text-foreground">{f.type}</span>
                        {f.scanner && <span className="text-xs text-muted-foreground">via {f.scanner}</span>}
                      </div>
                      <p className="text-xs text-muted-foreground">{f.detail}</p>
                      {f.matched_pattern && <p className="text-xs font-mono text-destructive mt-0.5">Pattern: {f.matched_pattern}</p>}
                    </div>
                  ))}
                </div>
              )}

              {step.redacted?.ptr && (
                <div className="mt-2">
                  <BlobViewer label="Redacted" pointer={step.redacted.ptr} data={step.redacted.data} emptyText="Redacted content not yet resolved" />
                </div>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Compact Event Timeline — chips, not full rows
// ---------------------------------------------------------------------------

function CompactTimeline({ job }: { job: Job }) {
  const entries = useMemo(() => {
    const items: { time: string; label: string; detail?: string; variant: BadgeVariant }[] = [];

    if (job.createdAt) {
      items.push({ time: job.createdAt, label: "Submitted", detail: job.topic, variant: "muted" });
    }
    if (job.safetyDecision?.type) {
      const variant: BadgeVariant = job.safetyDecision.type === "allow" || job.safetyDecision.type === "allow_with_constraints" ? "healthy"
        : job.safetyDecision.type === "deny" ? "governance" : "warning";
      items.push({ time: job.createdAt, label: `Safety: ${job.safetyDecision.type}`, detail: job.safetyDecision.matchedRule, variant });
    }
    if (job.approvalAt) {
      items.push({ time: new Date(job.approvalAt).toISOString(), label: `Approved by ${job.approvalBy ?? "operator"}`, variant: "cordum" });
    }
    if (job.output_safety?.decision) {
      const variant: BadgeVariant = job.output_safety.decision === "ALLOW" ? "healthy" : job.output_safety.decision === "QUARANTINE" ? "danger" : "warning";
      items.push({ time: job.updatedAt, label: `Output: ${job.output_safety.decision}`, detail: job.output_safety.reason, variant });
    }
    if (job.errorMessage || job.status === "failed") {
      items.push({ time: job.updatedAt, label: "Error", detail: job.errorMessage || `Code: ${job.errorCode || "unknown"}`, variant: "danger" });
    }

    const terminal = new Set(["succeeded", "completed", "failed", "cancelled", "denied", "timeout", "timed_out"]);
    const statusStr = job.status as string;
    if (terminal.has(statusStr)) {
      const label = statusStr === "completed" ? "Succeeded" : statusStr.charAt(0).toUpperCase() + statusStr.slice(1);
      const variant: BadgeVariant = statusStr === "succeeded" || statusStr === "completed" ? "cordum" : statusStr === "denied" ? "governance" : "danger";
      items.push({ time: job.updatedAt, label, variant });
    }

    return items;
  }, [job]);

  if (entries.length === 0) return <p className="text-sm text-muted-foreground italic">No events recorded.</p>;

  return (
    <div className="space-y-2">
      {entries.map((entry, i) => (
        <div key={`${entry.label}-${i}`} className="flex items-start gap-3">
          <span className="text-[10px] font-mono text-muted-foreground shrink-0 w-[64px] pt-0.5 text-right">
            {new Date(entry.time).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}
          </span>
          <StatusBadge variant={entry.variant} className="shrink-0">{entry.label}</StatusBadge>
          {entry.detail && <span className="text-xs text-muted-foreground truncate">{entry.detail}</span>}
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// BlobViewer — Redis pointer + expandable data
// ---------------------------------------------------------------------------

const MAX_RESULT_DISPLAY = 100 * 1024;

function formatBlobData(data: unknown): string | null {
  if (data == null) return null;
  if (typeof data === "string") {
    try {
      const parsed = JSON.parse(data);
      if (typeof parsed === "object" && parsed !== null) return JSON.stringify(parsed, null, 2);
    } catch { /* not JSON */ }
    return data;
  }
  return JSON.stringify(data, null, 2);
}

function BlobViewer({ label, pointer, data, emptyText }: {
  label: string; pointer?: string; data?: unknown; emptyText: string;
}) {
  const [expanded, setExpanded] = useState(false);
  const [showFull, setShowFull] = useState(false);

  if (!pointer && data == null) {
    return <div className="surface-inset p-4 font-mono text-xs"><p className="text-muted-foreground italic">{emptyText}</p></div>;
  }

  const formatted = formatBlobData(data);
  const isTruncated = formatted != null && formatted.length > MAX_RESULT_DISPLAY && !showFull;
  const displayText = isTruncated ? formatted.slice(0, MAX_RESULT_DISPLAY) : formatted;

  return (
    <div className="space-y-3">
      {pointer && (
        <div className="surface-inset p-4 font-mono text-xs flex items-center justify-between gap-3">
          <div className="min-w-0">
            <span className="text-muted-foreground">{label} pointer: </span>
            <span className="text-foreground break-all">{pointer}</span>
          </div>
          {formatted && (
            <Button variant="outline" size="sm" className="shrink-0" onClick={() => setExpanded(!expanded)}>
              <Eye className="w-3 h-3 mr-1" />{expanded ? "Hide" : "Read"}
            </Button>
          )}
        </div>
      )}
      {(expanded || !pointer) && displayText && (
        <div>
          <CodeBlock language="json" copyable maxHeight={500}>{displayText}</CodeBlock>
          {isTruncated && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setShowFull(true)}
              className="mt-2 h-auto px-0 text-cordum hover:bg-transparent hover:text-cordum hover:underline"
            >
              Show full result ({Math.round((formatted?.length ?? 0) / 1024)}KB)
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// JobTerminal — live WebSocket events for a job
// ---------------------------------------------------------------------------

function JobTerminal({ job }: { job: Job }) {
  const events = useEventStore((s) => s.events);
  const jobEvents = useMemo(
    () =>
      events
        .filter((e) => {
          const p = e.payload ?? {};
          return (p.jobId ?? p.job_id) === job.id;
        })
        .reverse(),
    [events, job.id],
  );

  const hasResult = job.result != null;
  if (!hasResult && jobEvents.length === 0) {
    return (
      <p className="text-muted-foreground italic">
        {["running", "pending", "dispatched"].includes(job.status)
          ? "Waiting for output\u2026"
          : "No output recorded."}
      </p>
    );
  }

  return (
    <div className="space-y-2">
      {jobEvents.map((e) => (
        <div key={e.id} className="flex gap-3">
          <span className="text-muted-foreground shrink-0 w-[80px]">{new Date(e.timestamp).toLocaleTimeString()}</span>
          <span className="text-cordum shrink-0">[{e.type}]</span>
          <span className="text-foreground break-all">
            {(e.payload?.message as string) ?? (e.payload?.status as string) ?? JSON.stringify(e.payload)}
          </span>
        </div>
      ))}
      {hasResult && (
        <>
          {jobEvents.length > 0 && <div className="border-t border-border my-3" />}
          <div className="text-muted-foreground mb-1">--- Result ---</div>
          <CodeBlock title="Result" language="json">
            {typeof job.result === "string" ? job.result : JSON.stringify(job.result, null, 2)}
          </CodeBlock>
        </>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Metadata bar — compact identity strip
// ---------------------------------------------------------------------------

function MetadataBar({ job, navigate }: { job: Job; navigate: (path: string) => void }) {
  const { runId, sessionId, workflowId } = getJobParentRefs(job);
  const fields: [string, string | undefined, (() => void) | undefined][] = [
    ["Topic", job.topic, undefined],
    ["Tenant", job.tenant, undefined],
    ["Workflow", workflowId, workflowId ? () => navigate(`/workflows/${workflowId}/studio`) : undefined],
    ["Run", runId, workflowId && runId ? () => navigate(`/workflows/${workflowId}/runs/${runId}`) : undefined],
    ["Session", sessionId, sessionId ? () => navigate(`/copilot/sessions/${sessionId}`) : undefined],
    ["Trace", job.traceId, undefined],
    ["Attempts", job.attempts ? String(job.attempts) : undefined, undefined],
  ];

  const active = fields.filter(([, v]) => !!v);
  if (active.length === 0) return null;

  return (
    <div className="flex flex-wrap gap-x-5 gap-y-1.5 px-1">
      {active.map(([label, value, onClick]) => (
        <div key={label} className="flex items-center gap-1.5">
          <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">{label}</span>
          {onClick ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-auto px-0 font-mono text-cordum hover:bg-transparent hover:text-cordum hover:underline"
              onClick={onClick}
            >
              {value}
            </Button>
          ) : (
            <span className="text-xs font-mono text-foreground">{value}</span>
          )}
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main page
// ---------------------------------------------------------------------------

const ACTIVE_STATUSES = new Set(["running", "dispatched", "pending", "scheduled"]);
const TERMINAL_STATUSES = new Set(["succeeded", "failed", "cancelled", "denied", "timeout", "output_quarantined"]);

const container = {
  hidden: { opacity: 0 },
  visible: {
    opacity: 1,
    transition: {
      staggerChildren: 0.05,
    },
  },
};

const item = {
  hidden: { opacity: 0, y: 12 },
  visible: { opacity: 1, y: 0, transition: { duration: 0.3 } },
};

export default function JobDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();

  const { data: job, isLoading, isError, error, refetch } = useQuery({
    queryKey: ["job", id],
    queryFn: async () => {
      const res = await get<BackendJobDetail>(`/jobs/${id}`);
      return mapJobDetail(res);
    },
    enabled: !!id,
    refetchInterval: (query) => {
      const s = query.state.data?.status;
      return s && TERMINAL_STATUSES.has(s) ? false : 5_000;
    },
  });

  const isActive = !!job?.status && ACTIVE_STATUSES.has(job.status);
  const { formatted: elapsedFormatted } = useElapsedTimer(job?.createdAt, isActive);

  const copyId = () => {
    if (id) { navigator.clipboard.writeText(id); toast.success("Job ID copied"); }
  };
  const [activeTab, setActiveTab] = useState("overview");

  // --- Error state ---
  if (isError) {
    return <ErrorBanner message={error instanceof Error ? error.message : "Failed to load job details"} onRetry={() => void refetch()} />;
  }

  // --- Loading state ---
  if (isLoading) {
    return (
      <div className="space-y-6">
        <div className="flex items-center gap-3"><Skeleton className="h-8 w-20" /><Skeleton className="h-4 w-32" /></div>
        <Skeleton className="h-40 rounded-3xl" />
        <div className="grid grid-cols-1 lg:grid-cols-12 gap-6">
          <div className="lg:col-span-8"><Skeleton className="h-64 rounded-3xl" /></div>
          <div className="lg:col-span-4"><Skeleton className="h-64 rounded-3xl" /></div>
        </div>
      </div>
    );
  }

  // --- Not found ---
  if (!job) {
    return (
      <EmptyState
        icon={<AlertTriangle className="w-10 h-10" />}
        title="Job not found"
        description="The job may have been purged or the ID is invalid."
        action={(
          <Button variant="outline" size="sm" onClick={() => navigate("/jobs")}>
            <ArrowLeft className="w-3 h-3 mr-1" />Back to Jobs
          </Button>
        )}
      />
    );
  }

  // --- Main layout ---
  return (
    <div className="space-y-6">
      {/* Top bar */}
      <div className="flex items-center justify-between">
        <Button variant="ghost" size="sm" onClick={() => navigate("/jobs")}>
          <ArrowLeft className="w-4 h-4 mr-1" />Jobs
        </Button>
        <div className="flex items-center gap-2">
          <Button type="button" variant="ghost" size="icon" onClick={copyId} title="Copy Job ID" aria-label="Copy job ID">
            <Copy className="w-3.5 h-3.5" />
          </Button>
          <JobActions job={job} />
        </div>
      </div>

      {/* Safety bypass warning */}
      {job.labels?.safety_bypassed === "true" && (
        <motion.div initial={{ opacity: 0, y: -8 }} animate={{ opacity: 1, y: 0 }}>
          <InfoBanner variant="warning" title="Safety bypassed" icon={<Shield className="h-4 w-4" />}>
            This job was allowed via fail-open because the Safety Kernel was unavailable.
            {job.labels.safety_bypass_reason && <> Reason: {job.labels.safety_bypass_reason}</>}
          </InfoBanner>
        </motion.div>
      )}

      {/* 1. HERO BANNER */}
      <HeroBanner job={job} elapsed={elapsedFormatted} isActive={isActive} />

      <motion.div
        variants={container}
        initial="hidden"
        animate="visible"
        className="space-y-6"
      >
        <motion.div variants={item}>
          {/* Metadata strip */}
          <MetadataBar job={job} navigate={navigate} />
        </motion.div>

        {job.labels && Object.keys(job.labels).length > 0 && (
          <motion.div variants={item} className="flex flex-wrap gap-1.5">
            {Object.entries(job.labels).map(([k, v]) => (
              <StatusBadge key={k} variant="muted" className="font-mono">
                <span className="text-muted-foreground">{k}:</span> {v}
              </StatusBadge>
            ))}
          </motion.div>
        )}

        <motion.div variants={item}>
          <Tabs
            tabs={[
              { id: "overview", label: "Overview" },
              { id: "payload", label: "Payload" },
              { id: "activity", label: "Activity" },
              { id: "governance", label: "Governance" },
            ]}
            activeTab={activeTab}
            onChange={setActiveTab}
            ariaLabel="Job detail sections"
          />
        </motion.div>

        <AnimatePresence mode="wait">
          {activeTab === "overview" && (
            <motion.div
              key="overview"
              variants={container}
              initial="hidden"
              animate="visible"
              exit={{ opacity: 0, y: -8 }}
              className="grid grid-cols-1 gap-6 lg:grid-cols-12"
            >
              {/* 2. SMART CONTEXT */}
              <motion.div variants={item} className="lg:col-span-8">
                <SmartContext job={job} />
              </motion.div>

              {/* 3. SAFETY STORY */}
              <motion.div variants={item} className="lg:col-span-4">
                <div className="instrument-card h-full">
                  <div className="flex items-center gap-2 mb-6">
                    <Shield className="w-4 h-4 text-cordum" />
                    <h2 className="font-display font-semibold text-sm text-foreground">Safety Story</h2>
                  </div>
                  <SafetyTimeline job={job} />
                </div>
              </motion.div>

              {/* Error block */}
              {(job.errorMessage || job.status === "failed") && (
                <motion.div variants={item} className="lg:col-span-12">
                  <InfoBanner variant="error" title="Error">
                    <div className="space-y-2 text-destructive">
                      <p className="text-sm font-mono whitespace-pre-wrap">
                        {job.errorMessage || `Job failed (no error message). Code: ${job.errorCode || "unknown"}`}
                      </p>
                      {job.errorCode && (
                        <p className="text-xs font-mono text-muted-foreground">
                          Code: {job.errorCode} {job.errorCodeEnum ? `(${job.errorCodeEnum})` : ""}
                        </p>
                      )}
                    </div>
                  </InfoBanner>
                </motion.div>
              )}
            </motion.div>
          )}

          {activeTab === "activity" && (
            <motion.div
              key="activity"
              variants={container}
              initial="hidden"
              animate="visible"
              exit={{ opacity: 0, y: -8 }}
              className="grid grid-cols-1 gap-6 lg:grid-cols-12"
            >
              <motion.div variants={item} className="lg:col-span-12">
                <div className="instrument-card">
                  <div className="flex items-center gap-2 mb-4">
                    <Clock className="w-4 h-4 text-cordum" />
                    <h2 className="font-display font-semibold text-sm text-foreground">Activity Timeline</h2>
                  </div>
                  <CompactTimeline job={job} />
                </div>
              </motion.div>

              <motion.div variants={item} className="lg:col-span-12">
                <div className="instrument-card !p-0 overflow-hidden">
                  <div className="px-5 py-3 border-b border-border bg-surface-0/30">
                    <h2 className="font-display font-semibold text-xs text-muted-foreground uppercase tracking-widest">Execution Log</h2>
                  </div>
                  <div className="surface-inset p-5 font-mono text-xs text-foreground min-h-[300px] max-h-[600px] overflow-auto">
                    <JobTerminal job={job} />
                  </div>
                </div>
              </motion.div>
            </motion.div>
          )}

          {activeTab === "payload" && (
            <motion.div
              key="payload"
              variants={container}
              initial="hidden"
              animate="visible"
              exit={{ opacity: 0, y: -8 }}
              className="space-y-6"
            >
              <motion.div variants={item}>
                <CollapsibleSection title="Context payload" defaultOpen={true}>
                  <div className="instrument-card">
                    <BlobViewer label="Context" pointer={job.contextPtr} data={job.context} emptyText="No context data available" />
                  </div>
                </CollapsibleSection>
              </motion.div>

              <motion.div variants={item}>
                <CollapsibleSection title="Raw output" defaultOpen={true}>
                  <div className="instrument-card">
                    <BlobViewer
                      label="Result"
                      pointer={job.resultPtr}
                      data={job.result}
                      emptyText={isActive ? "Job is still running\u2026" : "No result data available"}
                    />
                  </div>
                </CollapsibleSection>
              </motion.div>
            </motion.div>
          )}

          {activeTab === "governance" && (
            <motion.div
              key="governance"
              variants={item}
              initial="hidden"
              animate="visible"
              exit={{ opacity: 0, y: -8 }}
            >
              <GovernanceTimeline jobId={job.id} />
            </motion.div>
          )}
        </AnimatePresence>
      </motion.div>
    </div>
  );
}

