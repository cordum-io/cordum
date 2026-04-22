import { useMemo } from "react";
import { Link } from "react-router-dom";
import { AlertOctagon, ArrowRight, X } from "lucide-react";
import { cn } from "@/lib/utils";
import {
  useAuditVerify,
  type AuditVerifyGap,
} from "@/hooks/useAuditVerify";
import { useVerificationStore } from "@/state/verification";
import { summariseGaps, buildAuditDrillDownHref } from "./ChainIntegrityWidget";

// GapAlertBanner sits above the PolicyOverview page when the most
// recent chain verification returned status=compromised. It's
// session-dismissible via the Zustand verification slice so a
// compliance reviewer who has acknowledged the alert can keep working
// without the banner blocking their scroll depth, but the dismissal
// is NOT persisted to localStorage — on next login the banner
// re-appears until the underlying issue is resolved (verification
// status moves back to 'ok').

export interface GapAlertBannerProps {
  tenant: string;
  className?: string;
}

function firstTamperGap(gaps: AuditVerifyGap[]): AuditVerifyGap | null {
  for (const g of gaps) {
    if (g.type === "missing" || g.type === "hash_mismatch" || g.type === "out_of_order") {
      return g;
    }
  }
  return null;
}

export function GapAlertBanner({ tenant, className }: GapAlertBannerProps) {
  const query = useAuditVerify({ tenant });
  const dismissed = useVerificationStore(
    (s) => Boolean(s.dismissedGapBanners[tenant]),
  );
  const dismiss = useVerificationStore((s) => s.dismissGapBanner);

  const summary = useMemo(
    () => (query.data ? summariseGaps(query.data.gaps ?? []) : null),
    [query.data],
  );

  if (!query.data) return null;
  if (query.data.status !== "compromised") return null;
  if (dismissed) return null;
  if (!summary || summary.tamperTotal === 0) return null;

  const firstGap = firstTamperGap(query.data.gaps ?? []);
  const drillHref = buildAuditDrillDownHref(summary);

  return (
    <div
      role="alert"
      aria-live="assertive"
      data-testid="gap-alert-banner"
      className={cn(
        "relative overflow-hidden rounded-2xl border border-danger/30 bg-danger/10 p-4 shadow-sm",
        className,
      )}
    >
      {/* Scanning stripe — subtle animation cue that the system is
          flagging a live issue without being loud. */}
      <span
        aria-hidden="true"
        className="pointer-events-none absolute inset-x-0 top-0 h-0.5 bg-gradient-to-r from-transparent via-danger to-transparent"
      />
      <div className="flex items-start gap-3">
        <span
          className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-danger/40 bg-danger/20 text-danger"
          aria-hidden="true"
        >
          <AlertOctagon className="h-4 w-4" />
        </span>
        <div className="min-w-0 flex-1">
          <p className="font-display text-sm font-semibold text-danger">
            Audit chain integrity issue detected
            {firstGap ? (
              <>
                {" "}
                at <span className="font-mono">seq #{firstGap.at_seq}</span>
              </>
            ) : null}
            .
          </p>
          <p className="mt-1 text-xs text-danger/90">
            {summary.tamperTotal} tamper signal
            {summary.tamperTotal === 1 ? "" : "s"} across the retained window.
            Investigate in the Audit Log before publishing new policy.
          </p>
          <div className="mt-3 flex flex-wrap items-center gap-3">
            <Link
              to={drillHref}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-full border border-danger/40 bg-background/60 px-3 py-1.5 text-xs font-semibold text-danger",
                "transition-colors hover:bg-danger/15",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-danger/40",
              )}
            >
              Open Audit Log
              <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
            </Link>
            <Link
              to="/govern/verification"
              className="text-xs font-medium text-danger/90 underline decoration-dotted underline-offset-2 hover:text-danger"
            >
              View verification dashboard
            </Link>
          </div>
        </div>
        <button
          type="button"
          onClick={() => dismiss(tenant)}
          aria-label="Dismiss audit chain integrity banner for this session"
          className={cn(
            "ml-2 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-danger/80",
            "hover:bg-danger/15 hover:text-danger",
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-danger/40",
          )}
        >
          <X className="h-3.5 w-3.5" aria-hidden="true" />
        </button>
      </div>
    </div>
  );
}
