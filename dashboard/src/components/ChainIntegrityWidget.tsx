import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import {
  AlertOctagon,
  ArrowRight,
  Clock,
  Link2,
  RefreshCw,
  ShieldCheck,
  ShieldQuestion,
} from "lucide-react";
import { Card, CardTitle } from "./ui/Card";
import { Button } from "./ui/Button";
import { Skeleton } from "./ui/Skeleton";
import { cn, formatRelativeTime } from "../lib/utils";
import {
  useAuditVerify,
  useTriggerAuditVerify,
  type AuditVerifyResult,
  type AuditVerifyGap,
} from "../hooks/useAuditVerify";
import { useVerificationStore } from "../state/verification";
import { useIsAdmin } from "../hooks/usePermission";

// ---------------------------------------------------------------------------
// ChainIntegrityWidget
//
// A large governance-surface card that renders the current state of the
// tenant's audit-chain Merkle verification. Five resting states map to the
// gateway's VerifyResult.status with an additional NOT_CHECKED state for
// the first-load-before-any-data edge case (and for graceful degradation
// when the hook is disabled / errored out before producing a result).
//
// Design vocabulary: governance surface-card + ShieldCheck/ShieldQuestion/
// AlertOctagon icons from lucide + CSS-var tokens (success/warning/danger/
// muted). We lean into compliance-grade severity differentiation between
// partial (retention trimmed — expected, amber) and compromised (tamper
// detected — red). Retention trimming is normal day-to-day; tampering is
// a pager incident.
// ---------------------------------------------------------------------------

type WidgetState =
  | "loading"
  | "error"
  | "not_checked"
  | "ok"
  | "partial"
  | "compromised";

interface Tone {
  badge: string;
  tone: "success" | "warning" | "danger" | "muted";
  accentClass: string;
  bgTintClass: string;
  borderTintClass: string;
  iconClass: string;
  badgeClass: string;
}

const TONES: Record<Tone["tone"], Tone> = {
  success: {
    badge: "VERIFIED",
    tone: "success",
    accentClass: "bg-success",
    bgTintClass: "bg-success/5",
    borderTintClass: "border-success/25",
    iconClass: "text-success",
    badgeClass: "bg-success/10 text-success border-success/25",
  },
  warning: {
    badge: "RETENTION TRIMMED",
    tone: "warning",
    accentClass: "bg-warning",
    bgTintClass: "bg-warning/5",
    borderTintClass: "border-warning/25",
    iconClass: "text-warning",
    badgeClass: "bg-warning/10 text-warning border-warning/25",
  },
  danger: {
    badge: "CHAIN COMPROMISED",
    tone: "danger",
    accentClass: "bg-danger",
    bgTintClass: "bg-danger/5",
    borderTintClass: "border-danger/30",
    iconClass: "text-danger",
    badgeClass: "bg-danger/10 text-danger border-danger/30",
  },
  muted: {
    badge: "NOT VERIFIED",
    tone: "muted",
    accentClass: "bg-border",
    bgTintClass: "",
    borderTintClass: "border-border",
    iconClass: "text-muted-foreground",
    badgeClass: "bg-muted text-muted-foreground border-border",
  },
};

export interface ChainIntegrityWidgetProps {
  tenant: string;
  className?: string;
  /**
   * When true, renders a single horizontal bar (badge + tenant +
   * verified-count + last-verified + re-verify button) for use in a
   * page header context (e.g. the Audit Log top bar). Defaults to
   * false (full card).
   */
  compact?: boolean;
}

// ---------------------------------------------------------------------------
// Pure helpers (exported for test coverage without React renders)
// ---------------------------------------------------------------------------

export function deriveWidgetState(input: {
  isLoading: boolean;
  isFetching: boolean;
  isError: boolean;
  data: AuditVerifyResult | undefined;
}): WidgetState {
  if (input.isLoading && !input.data) return "loading";
  if (input.isError && !input.data) return "error";
  if (!input.data) return "not_checked";
  return input.data.status;
}

export interface GapSummary {
  missing: number;
  hashMismatch: number;
  outOfOrder: number;
  retentionTrimmed: number;
  tamperTotal: number;
  minTamperSeq: number | null;
  maxTamperSeq: number | null;
  minAnySeq: number | null;
  maxAnySeq: number | null;
}

export function summariseGaps(gaps: AuditVerifyGap[]): GapSummary {
  let missing = 0;
  let hashMismatch = 0;
  let outOfOrder = 0;
  let retentionTrimmed = 0;
  let minTamperSeq: number | null = null;
  let maxTamperSeq: number | null = null;
  let minAnySeq: number | null = null;
  let maxAnySeq: number | null = null;
  for (const g of gaps) {
    switch (g.type) {
      case "missing":
        missing += 1;
        break;
      case "hash_mismatch":
        hashMismatch += 1;
        break;
      case "out_of_order":
        outOfOrder += 1;
        break;
      case "retention_trimmed":
        retentionTrimmed += 1;
        break;
    }
    const isTamper =
      g.type === "missing" ||
      g.type === "hash_mismatch" ||
      g.type === "out_of_order";
    if (isTamper) {
      if (minTamperSeq === null || g.at_seq < minTamperSeq) minTamperSeq = g.at_seq;
      if (maxTamperSeq === null || g.at_seq > maxTamperSeq) maxTamperSeq = g.at_seq;
    }
    if (minAnySeq === null || g.at_seq < minAnySeq) minAnySeq = g.at_seq;
    if (maxAnySeq === null || g.at_seq > maxAnySeq) maxAnySeq = g.at_seq;
  }
  return {
    missing,
    hashMismatch,
    outOfOrder,
    retentionTrimmed,
    tamperTotal: missing + hashMismatch + outOfOrder,
    minTamperSeq,
    maxTamperSeq,
    minAnySeq,
    maxAnySeq,
  };
}

export function buildAuditDrillDownHref(summary: GapSummary): string {
  if (summary.minTamperSeq === null || summary.maxTamperSeq === null) {
    return "/audit";
  }
  const params = new URLSearchParams();
  params.set("seqFrom", String(summary.minTamperSeq));
  params.set("seqTo", String(summary.maxTamperSeq));
  return `/audit?${params.toString()}`;
}

// ---------------------------------------------------------------------------
// Visual: chain link glyph — same vocabulary as AuditChainCard's motif,
// scaled up for this larger widget.
// ---------------------------------------------------------------------------

function LinkMotif({
  tone,
  broken,
}: {
  tone: Tone["tone"];
  broken: boolean;
}) {
  const iconClass = cn("h-4 w-4", TONES[tone].iconClass);
  const dividerClass = cn(
    "h-px w-3 rounded-full",
    tone === "success" && "bg-success/50",
    tone === "danger" && "bg-danger/60",
    tone === "warning" && "bg-warning/50",
    tone === "muted" && "bg-border",
  );
  return (
    <span
      className="inline-flex items-center gap-0.5"
      aria-hidden="true"
      data-testid="chain-link-motif"
      data-broken={broken ? "true" : "false"}
    >
      <Link2 className={iconClass} />
      <span className={dividerClass} />
      {broken ? (
        <AlertOctagon className={cn("h-4 w-4 text-danger")} />
      ) : (
        <Link2 className={iconClass} />
      )}
      <span className={dividerClass} />
      <Link2 className={iconClass} />
    </span>
  );
}

// ---------------------------------------------------------------------------
// Last-verified tooltip / text
// ---------------------------------------------------------------------------

function formatLastVerified(ms: number | undefined): {
  label: string;
  title: string;
} {
  if (!ms) return { label: "Never", title: "Chain has not been verified in this session" };
  const d = new Date(ms);
  return {
    label: formatRelativeTime(d.toISOString()),
    title: `${d.toLocaleString()} (${d.toISOString()})`,
  };
}

// ---------------------------------------------------------------------------
// ChainIntegrityWidget — main render
// ---------------------------------------------------------------------------

export function ChainIntegrityWidget({
  tenant,
  className,
  compact = false,
}: ChainIntegrityWidgetProps) {
  const isAdmin = useIsAdmin();

  // sessionTriggered gates the network fetch. Defaults to false so a
  // page open never auto-hits the admin-only /audit/verify endpoint.
  // Admin click on "Run chain check" or "Re-verify" flips it true;
  // the flag stays true for the rest of the tab session so renders
  // that follow a refresh continue to show fresh poll results.
  const [sessionTriggered, setSessionTriggered] = useState(false);

  // Non-admins are hard-gated here too — the admin guard in
  // useAuditVerify is defence-in-depth; this local gate stops us
  // from rendering "loading" spinners for viewers who will never
  // get data.
  const queryEnabled = sessionTriggered && isAdmin;

  const query = useAuditVerify({ tenant, enabled: queryEnabled });
  const triggerVerify = useTriggerAuditVerify(tenant);
  const lastVerifiedAt = useVerificationStore(
    (s) => s.lastVerifiedAt[tenant] ?? undefined,
  );

  const state = deriveWidgetState({
    isLoading: query.isLoading && queryEnabled,
    isFetching: query.isFetching,
    isError: query.isError,
    data: query.data,
  });

  const summary = useMemo(
    () => (query.data ? summariseGaps(query.data.gaps ?? []) : null),
    [query.data],
  );

  const lastVerified = formatLastVerified(lastVerifiedAt);
  const reVerifyDisabled = query.isFetching;

  const handleReverify = () => {
    if (!isAdmin) return;
    setSessionTriggered(true);
    triggerVerify();
  };

  if (compact) {
    return (
      <CompactView
        state={state}
        data={query.data}
        tenant={tenant}
        isAdmin={isAdmin}
        isBusy={reVerifyDisabled}
        onAction={handleReverify}
        lastVerifiedLabel={lastVerified.label}
        lastVerifiedTitle={lastVerified.title}
        className={className}
      />
    );
  }

  if (state === "loading") {
    return (
      <LoadingView
        tenant={tenant}
        className={className}
        isAdmin={isAdmin}
      />
    );
  }

  if (state === "error") {
    return (
      <ErrorView
        tenant={tenant}
        errorMessage={
          query.error instanceof Error
            ? query.error.message
            : "Unable to reach the audit verification service."
        }
        isAdmin={isAdmin}
        isRetrying={reVerifyDisabled}
        onRetry={handleReverify}
        className={className}
      />
    );
  }

  if (state === "not_checked") {
    return (
      <NotCheckedView
        tenant={tenant}
        isAdmin={isAdmin}
        isBusy={reVerifyDisabled}
        onRun={handleReverify}
        className={className}
      />
    );
  }

  const data = query.data!;
  const s = summary!;
  const drillDownHref = buildAuditDrillDownHref(s);
  const toneKey: Tone["tone"] =
    state === "ok" ? "success" : state === "partial" ? "warning" : "danger";
  const tone = TONES[toneKey];
  const broken = state === "compromised";

  return (
    <Card
      className={cn("relative overflow-hidden", className)}
      data-testid="chain-integrity-widget"
      data-state={state}
    >
      {/* Hairline accent strip */}
      <span
        aria-hidden="true"
        className={cn(
          "pointer-events-none absolute inset-x-0 top-0 h-1",
          tone.accentClass,
        )}
      />

      {/* Header: title, chain motif, status badge */}
      <header className="mb-5 flex items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <CardTitle className="text-base">Audit chain integrity</CardTitle>
            <LinkMotif tone={toneKey} broken={broken} />
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            Tenant <span className="font-mono text-ink">{tenant || "default"}</span>
          </p>
        </div>
        <span
          className={cn(
            "inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11px] font-semibold tracking-[0.12em]",
            tone.badgeClass,
          )}
          role="status"
          aria-label={
            state === "ok"
              ? "Audit chain verified"
              : state === "partial"
                ? "Audit chain partially verified, older events trimmed by retention"
                : "Audit chain integrity check FAILED. Tampering detected."
          }
        >
          {state === "ok" ? (
            <ShieldCheck className="h-3.5 w-3.5" aria-hidden="true" />
          ) : state === "partial" ? (
            <ShieldQuestion className="h-3.5 w-3.5" aria-hidden="true" />
          ) : (
            <AlertOctagon className="h-3.5 w-3.5" aria-hidden="true" />
          )}
          {tone.badge}
        </span>
      </header>

      {/* Primary metric row — stacks vertically at 375px, wraps on sm+ */}
      <section
        className={cn(
          "mb-5 rounded-2xl border p-4 shadow-inner",
          tone.bgTintClass,
          tone.borderTintClass,
        )}
      >
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3 sm:items-end">
          <div>
            <p className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
              Verified events
            </p>
            <p className="mt-1 font-display text-3xl font-semibold leading-none text-ink tabular-nums">
              {data.verified_events}
              <span className="ml-1 text-sm font-normal text-muted-foreground">
                / {data.total_events}
              </span>
            </p>
          </div>
          <SeqRangeCell first={data.first_seq} last={data.last_seq} />
          <RetentionCell
            retentionBoundarySeq={data.retention_boundary_seq}
            retentionWindowHours={data.retention_window_hours}
          />
        </div>
        <StateFootnote state={state} summary={s} />
      </section>

      {/* Compromised-state severity banner */}
      {state === "compromised" && s.tamperTotal > 0 && (
        <section
          role="alert"
          aria-live="assertive"
          className={cn(
            "mb-5 rounded-2xl border border-danger/30 bg-danger/10 p-4",
          )}
        >
          <div className="flex items-start gap-3">
            <span className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-danger/40 bg-danger/20 text-danger">
              <AlertOctagon className="h-4 w-4" aria-hidden="true" />
            </span>
            <div className="min-w-0 flex-1">
              <h4 className="font-display text-sm font-semibold text-danger">
                Audit chain integrity check FAILED
              </h4>
              <p className="mt-1 text-xs text-danger/90">
                Tampering signals detected. Investigate immediately — compliance
                exports derived from this window cannot be attested until the
                chain is reconciled.
              </p>
              <dl className="mt-3 flex flex-wrap gap-x-5 gap-y-2 text-[11px]">
                {s.missing > 0 && (
                  <GapStat label="Missing" value={s.missing} />
                )}
                {s.hashMismatch > 0 && (
                  <GapStat label="Hash mismatch" value={s.hashMismatch} />
                )}
                {s.outOfOrder > 0 && (
                  <GapStat label="Out of order" value={s.outOfOrder} />
                )}
              </dl>
              {s.minTamperSeq !== null && s.maxTamperSeq !== null && (
                <p className="mt-3 font-mono text-[11px] text-danger/90">
                  Affected seq range: #{s.minTamperSeq} … #{s.maxTamperSeq}
                </p>
              )}
              <div className="mt-4">
                <Link
                  to={drillDownHref}
                  className={cn(
                    "inline-flex items-center gap-1.5 rounded-full border border-danger/40 bg-background/60 px-3 py-1.5 text-xs font-semibold text-danger",
                    "transition-colors hover:bg-danger/15",
                    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-danger/40",
                  )}
                >
                  Review in Audit Log
                  <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
                </Link>
              </div>
            </div>
          </div>
        </section>
      )}

      {/* Footer: last-verified + re-verify */}
      <footer className="flex flex-wrap items-center justify-between gap-3 border-t border-border/60 pt-4">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <Clock className="h-3.5 w-3.5" aria-hidden="true" />
          <span>
            Last verified{" "}
            <span
              className="font-medium text-ink underline decoration-dotted decoration-muted-foreground/60 underline-offset-2"
              title={lastVerified.title}
            >
              {lastVerified.label}
            </span>
          </span>
        </div>
        {isAdmin ? (
          <Button
            variant="outline"
            size="sm"
            onClick={handleReverify}
            disabled={reVerifyDisabled}
            aria-label="Re-verify the audit chain now"
          >
            <RefreshCw
              className={cn("h-3.5 w-3.5", reVerifyDisabled && "animate-spin")}
              aria-hidden="true"
            />
            Re-verify
          </Button>
        ) : (
          <span className="text-[11px] text-muted-foreground">
            Re-verify requires an admin role
          </span>
        )}
      </footer>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Subviews
// ---------------------------------------------------------------------------

interface CompactViewProps {
  state: WidgetState;
  data: AuditVerifyResult | undefined;
  tenant: string;
  isAdmin: boolean;
  isBusy: boolean;
  onAction: () => void;
  lastVerifiedLabel: string;
  lastVerifiedTitle: string;
  className?: string;
}

function CompactView({
  state,
  data,
  tenant,
  isAdmin,
  isBusy,
  onAction,
  lastVerifiedLabel,
  lastVerifiedTitle,
  className,
}: CompactViewProps) {
  const toneKey: Tone["tone"] =
    state === "ok"
      ? "success"
      : state === "partial"
        ? "warning"
        : state === "compromised"
          ? "danger"
          : "muted";
  const tone = TONES[toneKey];
  const stateLabel: string =
    state === "loading"
      ? "CHECKING…"
      : state === "error"
        ? "VERIFICATION UNAVAILABLE"
        : tone.badge;
  const actionLabel = state === "not_checked" ? "Run chain check" : "Re-verify";

  return (
    <div
      data-testid="chain-integrity-widget"
      data-state={state}
      data-compact="true"
      className={cn(
        "instrument-card flex flex-wrap items-center gap-3 px-4 py-2.5",
        className,
      )}
      role="status"
    >
      <span
        aria-hidden="true"
        className={cn("h-2 w-2 shrink-0 rounded-full", tone.accentClass)}
      />
      <span
        className={cn(
          "shrink-0 rounded-full border px-2 py-0.5 text-[10px] font-mono font-semibold uppercase tracking-[0.14em]",
          tone.badgeClass,
        )}
      >
        {stateLabel}
      </span>
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
        {data && (
          <span className="font-mono tabular-nums text-foreground">
            {data.verified_events} of {data.total_events} events
          </span>
        )}
        <span>
          Tenant{" "}
          <span className="font-mono text-foreground">
            {tenant || "default"}
          </span>
        </span>
        <span title={lastVerifiedTitle}>
          Last verified{" "}
          <span className="font-medium text-foreground">
            {lastVerifiedLabel}
          </span>
        </span>
      </div>
      {isAdmin ? (
        <Button
          variant="outline"
          size="sm"
          onClick={onAction}
          disabled={isBusy}
          aria-label={actionLabel}
        >
          <RefreshCw
            className={cn("h-3 w-3", isBusy && "animate-spin")}
            aria-hidden="true"
          />
          {actionLabel}
        </Button>
      ) : (
        <span className="shrink-0 text-[11px] text-muted-foreground">
          Admin only
        </span>
      )}
    </div>
  );
}

function LoadingView({
  tenant,
  isAdmin,
  className,
}: {
  tenant: string;
  isAdmin: boolean;
  className?: string;
}) {
  return (
    <Card
      className={cn("relative overflow-hidden", className)}
      data-testid="chain-integrity-widget"
      data-state="loading"
      aria-busy="true"
    >
      <span
        aria-hidden="true"
        className="pointer-events-none absolute inset-x-0 top-0 h-1 skeleton"
      />
      <header className="mb-5 flex items-start justify-between gap-4">
        <div className="min-w-0">
          <CardTitle className="text-base">Audit chain integrity</CardTitle>
          <p className="mt-1 text-xs text-muted-foreground">
            Tenant <span className="font-mono text-ink">{tenant || "default"}</span>
          </p>
        </div>
        <Skeleton className="h-6 w-28 rounded-full" />
      </header>
      <div
        role="status"
        aria-live="polite"
        className="mb-5 rounded-2xl border border-border p-4"
      >
        <Skeleton className="h-6 w-40" />
        <div className="mt-3 flex gap-6">
          <Skeleton className="h-4 w-24" />
          <Skeleton className="h-4 w-32" />
        </div>
        <p className="sr-only">Checking audit chain…</p>
      </div>
      <footer className="flex flex-wrap items-center justify-between gap-3 border-t border-border/60 pt-4 text-xs text-muted-foreground">
        <span className="inline-flex items-center gap-2">
          <RefreshCw className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
          Checking audit chain…
        </span>
        {!isAdmin && (
          <span className="text-[11px]">Re-verify requires an admin role</span>
        )}
      </footer>
    </Card>
  );
}

function ErrorView({
  tenant,
  errorMessage,
  isAdmin,
  isRetrying,
  onRetry,
  className,
}: {
  tenant: string;
  errorMessage: string;
  isAdmin: boolean;
  isRetrying: boolean;
  onRetry: () => void;
  className?: string;
}) {
  return (
    <Card
      className={cn("relative overflow-hidden", className)}
      data-testid="chain-integrity-widget"
      data-state="error"
    >
      <span
        aria-hidden="true"
        className="pointer-events-none absolute inset-x-0 top-0 h-1 bg-warning"
      />
      <header className="mb-4 flex items-start justify-between gap-4">
        <div>
          <CardTitle className="text-base">Audit chain integrity</CardTitle>
          <p className="mt-1 text-xs text-muted-foreground">
            Tenant <span className="font-mono text-ink">{tenant || "default"}</span>
          </p>
        </div>
      </header>
      <div
        role="alert"
        className="rounded-2xl border border-warning/30 bg-warning/10 p-4"
      >
        <div className="flex items-start gap-3">
          <AlertOctagon className="mt-0.5 h-4 w-4 shrink-0 text-warning" aria-hidden="true" />
          <div className="min-w-0">
            <h4 className="font-display text-sm font-semibold text-warning">
              Verification unavailable
            </h4>
            <p className="mt-1 break-words text-xs text-warning/90">{errorMessage}</p>
          </div>
        </div>
      </div>
      <footer className="mt-4 flex items-center justify-end">
        {isAdmin && (
          <Button
            variant="outline"
            size="sm"
            onClick={onRetry}
            disabled={isRetrying}
          >
            <RefreshCw
              className={cn("h-3.5 w-3.5", isRetrying && "animate-spin")}
              aria-hidden="true"
            />
            Retry
          </Button>
        )}
      </footer>
    </Card>
  );
}

function NotCheckedView({
  tenant,
  isAdmin,
  isBusy,
  onRun,
  className,
}: {
  tenant: string;
  isAdmin: boolean;
  isBusy: boolean;
  onRun: () => void;
  className?: string;
}) {
  return (
    <Card
      className={cn("relative overflow-hidden", className)}
      data-testid="chain-integrity-widget"
      data-state="not_checked"
    >
      <span
        aria-hidden="true"
        className="pointer-events-none absolute inset-x-0 top-0 h-1 bg-border"
      />
      <header className="mb-5 flex items-start justify-between gap-4">
        <div>
          <div className="flex items-center gap-2">
            <CardTitle className="text-base">Audit chain integrity</CardTitle>
            <LinkMotif tone="muted" broken={false} />
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            Tenant <span className="font-mono text-ink">{tenant || "default"}</span>
          </p>
        </div>
        <span
          className={cn(
            "inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11px] font-semibold tracking-[0.12em]",
            TONES.muted.badgeClass,
          )}
          role="status"
          aria-label="Audit chain not yet verified in this session"
        >
          <ShieldQuestion className="h-3.5 w-3.5" aria-hidden="true" />
          NOT VERIFIED
        </span>
      </header>
      <div className="flex flex-col items-center py-6 text-center">
        <p className="font-display text-sm font-semibold text-ink">
          No recent verification
        </p>
        <p className="mt-1 max-w-sm text-xs text-muted-foreground">
          Run a chain check to confirm every audit event between the earliest
          retained seq and now is cryptographically linked and untampered.
        </p>
        {isAdmin ? (
          <div className="mt-4">
            <Button variant="primary" size="sm" onClick={onRun} disabled={isBusy}>
              <RefreshCw
                className={cn("h-3.5 w-3.5", isBusy && "animate-spin")}
                aria-hidden="true"
              />
              Run chain check
            </Button>
          </div>
        ) : (
          <p className="mt-4 text-[11px] text-muted-foreground">
            Chain checks can only be initiated by an admin.
          </p>
        )}
      </div>
    </Card>
  );
}

function SeqRangeCell({
  first,
  last,
}: {
  first?: number;
  last?: number;
}) {
  const hasRange = (first ?? 0) > 0 || (last ?? 0) > 0;
  return (
    <div>
      <p className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
        Seq range
      </p>
      <p className="mt-1 font-mono text-sm font-medium tabular-nums text-ink">
        {hasRange ? `#${first ?? 0} … #${last ?? 0}` : "—"}
      </p>
    </div>
  );
}

function RetentionCell({
  retentionBoundarySeq,
  retentionWindowHours,
}: {
  retentionBoundarySeq: number;
  retentionWindowHours?: number;
}) {
  const windowLabel = (() => {
    if (!retentionWindowHours) return "—";
    if (retentionWindowHours % 24 === 0) {
      const days = retentionWindowHours / 24;
      return `${days}-day window`;
    }
    return `${retentionWindowHours}h window`;
  })();
  return (
    <div>
      <p className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
        Retention
      </p>
      <p className="mt-1 text-sm font-medium text-ink">
        {windowLabel}
        <span className="ml-1 font-mono text-xs text-muted-foreground">
          (boundary #{retentionBoundarySeq})
        </span>
      </p>
    </div>
  );
}

function StateFootnote({
  state,
  summary,
}: {
  state: WidgetState;
  summary: GapSummary;
}) {
  if (state === "ok") {
    return (
      <p className="mt-3 text-xs text-muted-foreground">
        All events within the retention window are linked to a valid Merkle
        predecessor.
      </p>
    );
  }
  if (state === "partial") {
    return (
      <p className="mt-3 text-xs text-muted-foreground">
        <span className="font-medium text-warning">
          {summary.retentionTrimmed} event
          {summary.retentionTrimmed === 1 ? "" : "s"}
        </span>{" "}
        trimmed by retention policy — expected for events older than the
        window. No tampering detected within the retained range.
      </p>
    );
  }
  if (state === "compromised") {
    return (
      <p className="mt-3 text-xs font-medium text-danger">
        {summary.tamperTotal} tamper signal
        {summary.tamperTotal === 1 ? "" : "s"} detected across{" "}
        {summary.minTamperSeq !== null && summary.maxTamperSeq !== null
          ? `seq #${summary.minTamperSeq} – #${summary.maxTamperSeq}`
          : "the retained range"}
        .
      </p>
    );
  }
  return null;
}

function GapStat({ label, value }: { label: string; value: number }) {
  return (
    <div className="flex items-center gap-1">
      <dt className="uppercase tracking-[0.12em] text-danger/80">{label}</dt>
      <dd className="font-mono font-semibold text-danger tabular-nums">{value}</dd>
    </div>
  );
}
