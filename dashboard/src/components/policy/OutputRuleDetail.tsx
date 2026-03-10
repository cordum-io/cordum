import { useMemo } from "react";
import { Link } from "react-router-dom";
import { useOutputRuleAudit } from "../../hooks/useOutputRules";
import type { OutputRule } from "../../types/policy";
import { Badge } from "../ui/Badge";
import { Drawer } from "../ui/Drawer";

function decisionVariant(decision?: string): "danger" | "warning" | "success" | "default" {
  switch ((decision || "").toLowerCase()) {
    case "deny":
    case "quarantine":
      return "danger";
    case "redact":
      return "warning";
    case "allow":
      return "success";
    default:
      return "default";
  }
}

function severityVariant(severity?: string): "danger" | "warning" | "info" | "default" {
  switch ((severity || "").toLowerCase()) {
    case "critical":
      return "danger";
    case "high":
      return "warning";
    case "medium":
      return "info";
    default:
      return "default";
  }
}

function formatTimestamp(value?: string): string {
  if (!value) return "Unknown";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function truncate(value: string, max = 120): string {
  if (value.length <= max) return value;
  return `${value.slice(0, max)}...`;
}

function computeWindowStats(timestamps: string[]) {
  const now = Date.now();
  const dayMs = 24 * 60 * 60 * 1000;
  let count24h = 0;
  let count7d = 0;
  let count30d = 0;
  for (const timestamp of timestamps) {
    const parsed = new Date(timestamp).getTime();
    if (Number.isNaN(parsed)) continue;
    const age = now - parsed;
    if (age <= dayMs) count24h += 1;
    if (age <= 7 * dayMs) count7d += 1;
    if (age <= 30 * dayMs) count30d += 1;
  }
  return { count24h, count7d, count30d };
}

export function OutputRuleDetail({
  rule,
  onClose,
}: {
  rule: OutputRule | null;
  onClose: () => void;
}) {
  const ruleID = rule?.id ?? "";
  const { data = [], isLoading, isError, isFetching } = useOutputRuleAudit(ruleID, 10);

  const stats = useMemo(
    () => computeWindowStats(data.map((entry) => entry.timestamp)),
    [data],
  );

  const configJson = useMemo(() => {
    if (!rule) return "";
    return JSON.stringify(
      {
        id: rule.id,
        description: rule.description,
        enabled: rule.enabled,
        decision: rule.decision,
        severity: rule.severity,
        reason: rule.reason,
        match: rule.match,
      },
      null,
      2,
    );
  }, [rule]);

  if (!rule) {
    return null;
  }

  return (
    <Drawer open={!!rule} onClose={onClose} size="xl">
      <div className="space-y-5">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h3 className="font-display text-xl font-semibold text-ink">{rule.id}</h3>
            <p className="text-sm text-muted-foreground">{rule.description || "Output safety rule details"}</p>
          </div>
          <div className="flex items-center gap-2">
            <Badge variant={decisionVariant(rule.decision)}>
              {(rule.decision || "allow").toUpperCase()}
            </Badge>
            <Badge variant={severityVariant(rule.severity)}>
              {(rule.severity || "low").toUpperCase()}
            </Badge>
            <Badge variant={rule.enabled ? "success" : "default"}>
              {rule.enabled ? "ENABLED" : "DISABLED"}
            </Badge>
          </div>
        </div>

        <div className="grid gap-3 sm:grid-cols-3">
          <div className="rounded-xl border border-border bg-surface2/30 p-3">
            <p className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Triggers 24h</p>
            <p className="mt-1 text-xl font-semibold text-ink">{stats.count24h}</p>
          </div>
          <div className="rounded-xl border border-border bg-surface2/30 p-3">
            <p className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Triggers 7d</p>
            <p className="mt-1 text-xl font-semibold text-ink">{stats.count7d}</p>
          </div>
          <div className="rounded-xl border border-border bg-surface2/30 p-3">
            <p className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Triggers 30d</p>
            <p className="mt-1 text-xl font-semibold text-ink">{stats.count30d}</p>
          </div>
        </div>

        <div className="grid gap-4 lg:grid-cols-2">
          <div className="space-y-3 rounded-xl border border-border p-4">
            <h4 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Scanners</h4>
            <div className="flex flex-wrap gap-1.5">
              {(rule.scanners ?? []).length === 0 && (
                <span className="text-xs text-muted-foreground">No scanners configured.</span>
              )}
              {(rule.scanners ?? []).map((scanner) => (
                <Badge key={scanner} variant="info">
                  {scanner}
                </Badge>
              ))}
            </div>
            <h4 className="pt-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Patterns</h4>
            <div className="space-y-1.5">
              {(rule.patterns ?? []).length === 0 && (
                <span className="text-xs text-muted-foreground">No content patterns configured.</span>
              )}
              {(rule.patterns ?? []).map((pattern, idx) => (
                <p
                  key={`${pattern}-${idx}`}
                  className="rounded-md bg-surface2 px-2 py-1 font-mono text-[11px] text-ink"
                >
                  {pattern}
                </p>
              ))}
            </div>
          </div>

          <div className="space-y-2 rounded-xl border border-border p-4">
            <h4 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Rule Configuration</h4>
            <pre className="max-h-64 overflow-auto rounded-md bg-surface2 p-3 text-[11px] text-ink">
              {configJson}
            </pre>
          </div>
        </div>

        <div className="space-y-3 rounded-xl border border-border p-4">
          <div className="flex items-center justify-between">
            <h4 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              Recent Findings (Last 10)
            </h4>
            {isFetching && <span className="text-xs text-muted-foreground">Refreshing…</span>}
          </div>

          {isLoading && (
            <div className="space-y-2">
              {Array.from({ length: 3 }, (_, idx) => (
                <div key={idx} className="h-12 animate-pulse rounded-lg bg-surface2" />
              ))}
            </div>
          )}

          {!isLoading && isError && (
            <p className="text-sm text-muted-foreground">Failed to load output findings for this rule.</p>
          )}

          {!isLoading && !isError && data.length === 0 && (
            <p className="text-sm text-muted-foreground">No quarantined output findings were recorded for this rule.</p>
          )}

          {!isLoading &&
            data.map((entry) => (
              <div key={entry.id} className="space-y-2 rounded-lg border border-border bg-surface2/20 p-3">
                <div className="flex flex-wrap items-center gap-2">
                  <Link
                    to={`/jobs/${encodeURIComponent(entry.jobId)}`}
                    className="font-mono text-xs font-semibold text-accent hover:underline"
                  >
                    {entry.jobId}
                  </Link>
                  <span className="text-xs text-muted-foreground">{formatTimestamp(entry.timestamp)}</span>
                  {entry.decision && (
                    <Badge variant={decisionVariant(entry.decision)}>
                      {entry.decision.toUpperCase()}
                    </Badge>
                  )}
                  {entry.phase && <Badge variant="default">{entry.phase.toUpperCase()}</Badge>}
                </div>

                {entry.reason && <p className="text-xs text-muted-foreground">{entry.reason}</p>}

                {(entry.findings ?? []).map((finding, idx) => (
                  <p key={`${entry.id}-${idx}`} className="text-xs text-ink">
                    <span className="font-semibold">{finding.type}</span>: {truncate(finding.detail)}
                  </p>
                ))}

                {(entry.findings ?? []).length === 0 && (
                  <p className="text-xs text-muted-foreground">No scanner findings payload was attached.</p>
                )}

                {(entry.originalPtr || entry.redactedPtr) && (
                  <p className="font-mono text-[11px] text-muted-foreground">
                    {entry.originalPtr ? `original=${entry.originalPtr}` : ""}
                    {entry.originalPtr && entry.redactedPtr ? " " : ""}
                    {entry.redactedPtr ? `redacted=${entry.redactedPtr}` : ""}
                  </p>
                )}
              </div>
            ))}
        </div>
      </div>
    </Drawer>
  );
}
