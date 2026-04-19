import { useMemo } from "react";
import { motion, AnimatePresence } from "framer-motion";
import {
  ChevronDown,
  Link2,
  Link2Off,
  RefreshCw,
  ShieldAlert,
  ShieldCheck,
  ShieldQuestion,
} from "lucide-react";
import { Card, CardHeader, CardTitle } from "./ui/Card";
import { cn, formatRelativeTime } from "../lib/utils";
import {
  useAuditChainVerify,
  type AuditVerifyGap,
  type AuditVerifyResult,
} from "../hooks/useAuditChainVerify";
import { useAuditChainUI } from "../state/auditChain";

// ---------------------------------------------------------------------------
// Status presentation model
//
// The card has three resting states that map directly to the gateway's
// VerifyResult.status field. Each state picks an icon, tone colour, glyph,
// and short human label — keeping the decision table in one place so the
// rendering code below stays declarative.
// ---------------------------------------------------------------------------

type Status = AuditVerifyResult["status"];

interface StatusConfig {
  label: string;
  badge: string; // short glyph for the compact pill
  tone: "success" | "danger" | "warning" | "muted";
  Icon: typeof ShieldCheck;
  // chainBroken toggles the visual "broken link" treatment in the
  // chain-of-three motif. We render two full links + one replacement
  // (broken / question-mark) so the silhouette is consistent.
  chainBroken: boolean;
}

const STATUS_CONFIG: Record<Status | "loading" | "error" | "unknown", StatusConfig> = {
  ok: {
    label: "Chain verified",
    badge: "✓",
    tone: "success",
    Icon: ShieldCheck,
    chainBroken: false,
  },
  compromised: {
    label: "Tampering detected",
    badge: "✗",
    tone: "danger",
    Icon: ShieldAlert,
    chainBroken: true,
  },
  partial: {
    label: "Retention trimmed",
    badge: "○",
    tone: "warning",
    Icon: ShieldQuestion,
    chainBroken: false,
  },
  loading: {
    label: "Verifying…",
    badge: "…",
    tone: "muted",
    Icon: RefreshCw,
    chainBroken: false,
  },
  error: {
    label: "Verify failed",
    badge: "!",
    tone: "warning",
    Icon: ShieldQuestion,
    chainBroken: true,
  },
  unknown: {
    label: "No data",
    badge: "–",
    tone: "muted",
    Icon: ShieldQuestion,
    chainBroken: false,
  },
};

const TONE_CLASSES: Record<StatusConfig["tone"], string> = {
  success: "bg-success/10 text-success border-success/25",
  danger: "bg-danger/10 text-danger border-danger/30",
  warning: "bg-warning/10 text-warning border-warning/25",
  muted: "bg-muted text-muted-foreground border-border",
};

// ---------------------------------------------------------------------------
// Gap classification → row tone
//
// Retention_trimmed is the "everything is fine, just old" row — it should
// never read as alarming. The other three map to distinct severities so an
// operator scanning the expanded panel can sort tampering from noise at a
// glance.
// ---------------------------------------------------------------------------

const GAP_TONE: Record<AuditVerifyGap["type"], StatusConfig["tone"]> = {
  retention_trimmed: "muted",
  missing: "danger",
  hash_mismatch: "danger",
  out_of_order: "warning",
};

const GAP_LABEL: Record<AuditVerifyGap["type"], string> = {
  retention_trimmed: "retention trimmed",
  missing: "missing",
  hash_mismatch: "hash mismatch",
  out_of_order: "out of order",
};

// ---------------------------------------------------------------------------
// summarizeGaps — groups gaps by type and caps each list at 10 seqs for
// display. Exported so the test file can pin the grouping behaviour
// without rendering the card.
// ---------------------------------------------------------------------------

export interface GapBucket {
  type: AuditVerifyGap["type"];
  seqs: number[];
  total: number;
  overflow: number;
}

export function summarizeGaps(gaps: AuditVerifyGap[]): GapBucket[] {
  const groups = new Map<AuditVerifyGap["type"], number[]>();
  for (const g of gaps) {
    const arr = groups.get(g.type) ?? [];
    arr.push(g.at_seq);
    groups.set(g.type, arr);
  }
  const order: AuditVerifyGap["type"][] = [
    "hash_mismatch",
    "missing",
    "out_of_order",
    "retention_trimmed",
  ];
  const out: GapBucket[] = [];
  for (const type of order) {
    const seqs = groups.get(type);
    if (!seqs || seqs.length === 0) continue;
    const total = seqs.length;
    const show = seqs.slice(0, 10);
    out.push({ type, seqs: show, total, overflow: Math.max(0, total - show.length) });
  }
  return out;
}

// ---------------------------------------------------------------------------
// deriveStatus — resolves the four-way status used by the card
// (ok / compromised / partial / loading+error+unknown) from react-query
// state. Exported for tests so the loading/error branches are pinned.
// ---------------------------------------------------------------------------

export function deriveStatus(input: {
  isLoading: boolean;
  isError: boolean;
  data: AuditVerifyResult | undefined;
}): { key: keyof typeof STATUS_CONFIG; data: AuditVerifyResult | undefined } {
  if (input.isLoading && !input.data) return { key: "loading", data: undefined };
  if (input.isError && !input.data) return { key: "error", data: undefined };
  if (!input.data) return { key: "unknown", data: undefined };
  return { key: input.data.status, data: input.data };
}

// ---------------------------------------------------------------------------
// ChainMotif — a three-link visual cue next to the title. Stable SVG so it
// renders identically in print / PDF export. When chainBroken is true the
// middle link is swapped for a Link2Off icon — the reader's eye catches
// the discontinuity immediately.
// ---------------------------------------------------------------------------

function ChainMotif({ tone, broken }: { tone: StatusConfig["tone"]; broken: boolean }) {
  const iconClass = cn(
    "h-3.5 w-3.5",
    tone === "success" && "text-success",
    tone === "danger" && "text-danger",
    tone === "warning" && "text-warning",
    tone === "muted" && "text-muted-foreground",
  );
  const dividerClass = cn(
    "h-px w-2 rounded-full",
    tone === "success" && "bg-success/60",
    tone === "danger" && "bg-danger/60",
    tone === "warning" && "bg-warning/60",
    tone === "muted" && "bg-border",
  );
  return (
    <span
      className="inline-flex items-center gap-0.5"
      aria-hidden="true"
      data-testid="chain-motif"
      data-broken={broken ? "true" : "false"}
    >
      <Link2 className={iconClass} />
      <span className={dividerClass} />
      {broken ? (
        <Link2Off className={cn(iconClass, "text-danger")} />
      ) : (
        <Link2 className={iconClass} />
      )}
      <span className={dividerClass} />
      <Link2 className={iconClass} />
    </span>
  );
}

// ---------------------------------------------------------------------------
// AuditChainCard — compact status badge with expandable gap details
// ---------------------------------------------------------------------------

export interface AuditChainCardProps {
  /** Tenant to verify. Empty string uses the CLI's default tenant. */
  tenant: string;
  /** Hide the expand toggle for layouts where the card is display-only. */
  collapsible?: boolean;
  className?: string;
}

export function AuditChainCard({
  tenant,
  collapsible = true,
  className,
}: AuditChainCardProps) {
  const query = useAuditChainVerify(tenant);
  const isExpanded = useAuditChainUI((s) => Boolean(s.expandedByTenant[tenant]));
  const toggleExpanded = useAuditChainUI((s) => s.toggleExpanded);

  const { key, data } = deriveStatus({
    isLoading: query.isLoading,
    isError: query.isError,
    data: query.data,
  });
  const config = STATUS_CONFIG[key];
  const buckets = useMemo(
    () => (data ? summarizeGaps(data.gaps ?? []) : []),
    [data],
  );

  const dataUpdatedAt = query.dataUpdatedAt;
  const lastVerified = dataUpdatedAt
    ? formatRelativeTime(new Date(dataUpdatedAt).toISOString())
    : "never";

  const isLoading = key === "loading";
  const Icon = config.Icon;

  const handleToggle = () => {
    if (!collapsible) return;
    if (!data) return; // nothing to expand
    toggleExpanded(tenant);
  };

  return (
    <Card className={cn("relative overflow-hidden", className)}>
      {/* Hairline accent strip — mirrors InstrumentCard convention so the
          card reads as part of the same governance surface family. */}
      <span
        aria-hidden="true"
        className={cn(
          "pointer-events-none absolute inset-x-0 top-0 h-0.5",
          config.tone === "success" && "bg-success",
          config.tone === "danger" && "bg-danger",
          config.tone === "warning" && "bg-warning",
          config.tone === "muted" && "bg-border",
        )}
      />

      <CardHeader>
        <div className="flex min-w-0 items-center gap-2">
          <Icon
            className={cn(
              "h-4 w-4 shrink-0",
              config.tone === "success" && "text-success",
              config.tone === "danger" && "text-danger",
              config.tone === "warning" && "text-warning",
              config.tone === "muted" && "text-muted-foreground",
              isLoading && "animate-spin",
            )}
            aria-hidden="true"
          />
          <CardTitle className="truncate text-sm">Audit chain</CardTitle>
          <ChainMotif tone={config.tone} broken={config.chainBroken} />
        </div>
        <StatusPill tone={config.tone} label={config.label} badge={config.badge} />
      </CardHeader>

      <SummaryRow data={data} tenant={tenant} lastVerified={lastVerified} />

      {collapsible && data && (
        <button
          type="button"
          onClick={handleToggle}
          aria-expanded={isExpanded}
          aria-label={isExpanded ? "Collapse gap details" : "Expand gap details"}
          className={cn(
            "mt-4 inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-medium",
            "border border-border bg-transparent text-muted-foreground",
            "transition-colors hover:bg-muted hover:text-ink",
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          )}
        >
          <motion.span
            initial={false}
            animate={{ rotate: isExpanded ? 180 : 0 }}
            transition={{ duration: 0.15 }}
            className="inline-flex"
          >
            <ChevronDown className="h-3.5 w-3.5" aria-hidden="true" />
          </motion.span>
          {isExpanded ? "Hide gap list" : `${data.gaps?.length ?? 0} gap${(data.gaps?.length ?? 0) === 1 ? "" : "s"}`}
        </button>
      )}

      <AnimatePresence initial={false}>
        {isExpanded && data && (
          <motion.div
            key="gaps"
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.2, ease: "easeOut" }}
            className="overflow-hidden"
          >
            <GapDetail buckets={buckets} data={data} />
          </motion.div>
        )}
      </AnimatePresence>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Internal presentational fragments
// ---------------------------------------------------------------------------

function StatusPill({
  tone,
  label,
  badge,
}: {
  tone: StatusConfig["tone"];
  label: string;
  badge: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs font-semibold",
        TONE_CLASSES[tone],
      )}
      role="status"
    >
      <span aria-hidden="true" className="font-mono text-[10px] leading-none">
        {badge}
      </span>
      {label}
    </span>
  );
}

function SummaryRow({
  data,
  tenant,
  lastVerified,
}: {
  data: AuditVerifyResult | undefined;
  tenant: string;
  lastVerified: string;
}) {
  const tenantLabel = tenant || "default";
  if (!data) {
    return (
      <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-xs text-muted-foreground">
        <SummaryCell term="Tenant" value={tenantLabel} />
        <SummaryCell term="Last verified" value={lastVerified} />
      </dl>
    );
  }
  const first = data.first_seq ?? 0;
  const last = data.last_seq ?? 0;
  const seqRange = first || last ? `${first}…${last}` : "none";
  return (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-xs text-muted-foreground">
      <SummaryCell term="Tenant" value={tenantLabel} />
      <SummaryCell term="Last verified" value={lastVerified} />
      <SummaryCell
        term="Events"
        value={`${data.verified_events}/${data.total_events} verified`}
      />
      <SummaryCell term="Seq range" value={seqRange} mono />
      <SummaryCell
        term="Retention boundary"
        value={`seq ${data.retention_boundary_seq}`}
        mono
      />
      {data.retention_window_hours ? (
        <SummaryCell
          term="Retention window"
          value={`${data.retention_window_hours.toFixed(0)}h`}
        />
      ) : (
        <span />
      )}
    </dl>
  );
}

function SummaryCell({
  term,
  value,
  mono,
}: {
  term: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-[10px] uppercase tracking-wider text-muted-foreground/70">
        {term}
      </dt>
      <dd
        className={cn(
          "truncate text-xs font-medium text-ink",
          mono && "font-mono",
        )}
      >
        {value}
      </dd>
    </div>
  );
}

function GapDetail({
  buckets,
  data,
}: {
  buckets: GapBucket[];
  data: AuditVerifyResult;
}) {
  if (buckets.length === 0) {
    return (
      <p className="mt-4 rounded-xl border border-border bg-muted/40 p-3 text-xs text-muted-foreground">
        No gaps observed. Every event between seq {data.first_seq ?? 0} and{" "}
        {data.last_seq ?? 0} verified against the chain.
      </p>
    );
  }
  return (
    <ul className="mt-4 space-y-2 border-t border-border/60 pt-3">
      {buckets.map((bucket) => (
        <li
          key={bucket.type}
          className={cn(
            "rounded-xl border px-3 py-2 text-xs",
            TONE_CLASSES[GAP_TONE[bucket.type]],
          )}
        >
          <div className="flex items-center justify-between">
            <span className="font-semibold capitalize">{GAP_LABEL[bucket.type]}</span>
            <span className="font-mono text-[11px]">
              {bucket.total} {bucket.total === 1 ? "seq" : "seqs"}
            </span>
          </div>
          <p className="mt-1 font-mono text-[11px] opacity-90">
            {bucket.seqs.map((s) => `#${s}`).join(", ")}
            {bucket.overflow > 0 && (
              <span className="opacity-70"> (+{bucket.overflow} more)</span>
            )}
          </p>
        </li>
      ))}
    </ul>
  );
}
