// GovernanceHealthIndicator — Command Center composite score widget.
//
// Renders a circular progress ring with the letter grade centred, title,
// and a factor breakdown panel. Color bands are green/amber/red; icons +
// numeric score are always both present so accessibility doesn't rely
// on colour alone. A 403 from the backend means the caller isn't an
// admin — the widget renders nothing and never shows an error toast.
import { motion } from "framer-motion";
import { ShieldCheck, ShieldAlert, AlertTriangle } from "lucide-react";
import { cn } from "../../lib/utils";
import type { GovernanceHealth } from "../../api/types";
import { useGovernanceHealth } from "../../hooks/useGovernanceHealth";

const BAND_CLASSES: Record<"green" | "amber" | "red", string> = {
  green: "text-emerald-500 border-emerald-500/30 bg-emerald-500/5",
  amber: "text-amber-500 border-amber-500/30 bg-amber-500/5",
  red: "text-red-500 border-red-500/30 bg-red-500/5",
};

const BAND_ICON: Record<"green" | "amber" | "red", typeof ShieldCheck> = {
  green: ShieldCheck,
  amber: AlertTriangle,
  red: ShieldAlert,
};

const FACTOR_TITLES: Record<string, string> = {
  denial_rate: "Denial rate (24h)",
  approval_latency_p95: "Approval latency p95",
  policy_coverage: "Policy coverage",
  chain_integrity: "Chain integrity",
};

function bandFromScore(score: number): "green" | "amber" | "red" {
  if (score >= 80) return "green";
  if (score >= 60) return "amber";
  return "red";
}

export function GovernanceHealthIndicator() {
  const { data, isLoading, error } = useGovernanceHealth();

  // Non-admin (403): render nothing — the widget is admin-only.
  if (error && (error as { status?: number } | null)?.status === 403) {
    return null;
  }

  if (isLoading) {
    return (
      <div
        className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white/50 dark:bg-gray-900/50 p-4"
        data-testid="governance-health-loading"
      >
        <div className="h-4 w-32 bg-gray-200 dark:bg-gray-700 rounded animate-pulse mb-3" />
        <div className="h-20 w-20 rounded-full bg-gray-200 dark:bg-gray-700 animate-pulse mx-auto" />
      </div>
    );
  }

  if (error || !data) {
    return (
      <div
        className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white/50 dark:bg-gray-900/50 p-4 text-sm text-gray-500"
        data-testid="governance-health-error"
      >
        Governance health unavailable.
      </div>
    );
  }

  return <GovernanceHealthCard health={data} />;
}

export function GovernanceHealthCard({ health }: { health: GovernanceHealth }) {
  const band = bandFromScore(health.score);
  const Icon = BAND_ICON[band];
  const bandClasses = BAND_CLASSES[band];
  const ring = Math.max(0, Math.min(100, health.score));
  const circumference = 2 * Math.PI * 36;
  const dashOffset = circumference - (ring / 100) * circumference;
  const describedBy = "governance-health-factors";

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3 }}
      role="status"
      aria-label={`Governance health score ${health.score} out of 100, grade ${health.grade}`}
      aria-describedby={describedBy}
      tabIndex={0}
      className={cn(
        "rounded-xl border p-4 bg-white dark:bg-gray-900",
        "border-gray-200 dark:border-gray-800",
      )}
      data-testid="governance-health"
    >
      <div className="flex items-center gap-2 mb-3">
        <Icon className={cn("w-4 h-4", bandClasses.split(" ").find((c) => c.startsWith("text-")))} aria-hidden />
        <h3 className="text-sm font-semibold">Governance Health</h3>
        {health.truncated_at_max && (
          <span className="ml-auto text-xs text-gray-500" title="Approximate — data-scan cap hit">
            approx.
          </span>
        )}
      </div>

      <div className="flex items-center gap-4 flex-wrap">
        <svg
          width="84"
          height="84"
          viewBox="0 0 84 84"
          aria-hidden
          className="w-16 h-16 sm:w-[84px] sm:h-[84px]"
        >
          <circle
            cx="42"
            cy="42"
            r="36"
            fill="none"
            stroke="currentColor"
            className="text-gray-200 dark:text-gray-800"
            strokeWidth="8"
          />
          <circle
            cx="42"
            cy="42"
            r="36"
            fill="none"
            stroke="currentColor"
            strokeWidth="8"
            strokeDasharray={circumference}
            strokeDashoffset={dashOffset}
            strokeLinecap="round"
            transform="rotate(-90 42 42)"
            className={bandClasses.split(" ").find((c) => c.startsWith("text-"))}
          />
          <text
            x="42"
            y="48"
            textAnchor="middle"
            className={cn("text-2xl font-bold", bandClasses.split(" ").find((c) => c.startsWith("text-")))}
            fill="currentColor"
          >
            {health.grade}
          </text>
        </svg>

        <div>
          <div className="text-3xl font-bold tabular-nums" data-testid="governance-health-score">
            {health.score}
          </div>
          <div className="text-xs text-gray-500">/ 100</div>
        </div>
      </div>

      <dl id={describedBy} className="mt-3 grid grid-cols-2 gap-2 text-xs">
        {Object.entries(health.factors).map(([name, factor]) => (
          <div key={name} className="flex flex-col">
            <dt className="text-gray-500 truncate" title={FACTOR_TITLES[name] ?? name}>
              {FACTOR_TITLES[name] ?? name}
            </dt>
            <dd className="font-semibold tabular-nums">
              {factor.notes ? <span className="text-gray-400">—</span> : factor.score}
            </dd>
          </div>
        ))}
      </dl>
    </motion.div>
  );
}
