import { useState, useCallback } from "react";
import { Shield, ShieldCheck, ShieldAlert, ChevronDown, ChevronUp, Loader } from "lucide-react";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { cn } from "../../lib/utils";
import type { AuditEntry } from "../../api/types";

// ---------------------------------------------------------------------------
// Hash verification helpers
// ---------------------------------------------------------------------------

async function computeEventHash(entry: AuditEntry): Promise<string> {
  const bundleIds = (entry.bundleIds ?? []).join(",");
  const payload = `${entry.action}|${entry.resourceType}|${entry.resourceId}|${bundleIds}|${entry.timestamp}`;
  const data = new TextEncoder().encode(payload);
  const hashBuffer = await crypto.subtle.digest("SHA-256", data);
  const hashArray = new Uint8Array(hashBuffer);
  // Backend uses hex.EncodeToString(sum[:6]) → 12 hex chars
  return Array.from(hashArray.slice(0, 6))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

function extractHashFromId(id: string): string | null {
  // ID format: {createdAt}-{12 hex chars}
  // createdAt is RFC3339 with dashes, so take the last 12 chars after the last dash
  const lastDash = id.lastIndexOf("-");
  if (lastDash === -1) return null;
  const hash = id.slice(lastDash + 1);
  if (hash.length !== 12 || !/^[0-9a-f]{12}$/.test(hash)) return null;
  return hash;
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface VerificationResult {
  total: number;
  hashMatches: number;
  hashMismatches: number;
  orderViolations: number;
  oldestDate: string;
  newestDate: string;
  skipped: number; // events without parseable hash
}

// ---------------------------------------------------------------------------
// AuditIntegrityPanel
// ---------------------------------------------------------------------------

export function AuditIntegrityPanel({ events }: { events: AuditEntry[] }) {
  const [expanded, setExpanded] = useState(false);
  const [verifying, setVerifying] = useState(false);
  const [progress, setProgress] = useState({ done: 0, total: 0 });
  const [result, setResult] = useState<VerificationResult | null>(null);

  const oldestEvent = events.length > 0 ? events[events.length - 1] : null;
  const newestEvent = events.length > 0 ? events[0] : null;

  const handleVerify = useCallback(async () => {
    if (events.length === 0) return;
    setVerifying(true);
    setResult(null);
    setProgress({ done: 0, total: events.length });

    let hashMatches = 0;
    let hashMismatches = 0;
    let orderViolations = 0;
    let skipped = 0;

    for (let i = 0; i < events.length; i++) {
      const entry = events[i];

      // Hash check
      const expectedHash = extractHashFromId(entry.id);
      if (!expectedHash) {
        skipped++;
      } else {
        const computed = await computeEventHash(entry);
        if (computed === expectedHash) {
          hashMatches++;
        } else {
          hashMismatches++;
        }
      }

      // Chronological order check (events are newest-first)
      if (i > 0) {
        const prevTime = new Date(events[i - 1].timestamp).getTime();
        const currTime = new Date(entry.timestamp).getTime();
        if (currTime > prevTime) {
          orderViolations++;
        }
      }

      // Update progress periodically
      if (i % 10 === 0 || i === events.length - 1) {
        setProgress({ done: i + 1, total: events.length });
        await new Promise((r) => setTimeout(r, 0)); // yield to UI
      }
    }

    setResult({
      total: events.length,
      hashMatches,
      hashMismatches,
      orderViolations,
      oldestDate: oldestEvent?.timestamp ?? "",
      newestDate: newestEvent?.timestamp ?? "",
      skipped,
    });
    setVerifying(false);
  }, [events, oldestEvent, newestEvent]);

  const passed = result && result.hashMismatches === 0 && result.orderViolations === 0;

  return (
    <div className="mt-2">
      {/* Toggle */}
      <button
        type="button"
        className="flex items-center gap-2 text-xs text-muted-foreground hover:text-ink transition"
        onClick={() => setExpanded((v) => !v)}
      >
        <Shield className="h-3.5 w-3.5" />
        Audit Trail Info
        {expanded ? <ChevronUp className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />}
      </button>

      {expanded && (
        <Card className="mt-2 space-y-4 p-4">
          {/* Retention section */}
          <div>
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-2">
              Retention
            </h3>
            <div className="space-y-1 text-sm text-ink">
              <p>
                Retention: <span className="font-medium">Most recent 500 events</span>
              </p>
              {oldestEvent && (
                <p>
                  Oldest event:{" "}
                  <span className="font-mono text-xs">
                    {new Date(oldestEvent.timestamp).toLocaleString()}
                  </span>
                </p>
              )}
              <p>
                Events loaded: <span className="font-medium">{events.length}</span>
              </p>
            </div>
            <p className="mt-2 text-xs italic text-muted-foreground">
              Audit storage currently retains the most recent 500 events. Contact your
              administrator to configure extended retention.
            </p>
          </div>

          {/* Immutability section */}
          <div>
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-2">
              Immutability
            </h3>
            <div className="flex items-start gap-2 rounded-lg border border-border bg-surface2/30 px-3 py-2.5">
              <Shield className="mt-0.5 h-4 w-4 shrink-0 text-accent" />
              <p className="text-sm text-ink">
                Audit events are append-only. Events cannot be modified or deleted, including
                by administrators.
              </p>
            </div>

            {/* Verify button */}
            <div className="mt-3">
              <Button
                variant="outline"
                size="sm"
                onClick={handleVerify}
                disabled={verifying || events.length === 0}
              >
                {verifying ? (
                  <Loader className="h-3.5 w-3.5 animate-spin" />
                ) : (
                  <Shield className="h-3.5 w-3.5" />
                )}
                {verifying ? "Verifying\u2026" : "Verify Integrity"}
              </Button>

              {/* Progress */}
              {verifying && (
                <div className="mt-2 flex items-center gap-2">
                  <div className="h-1.5 w-32 overflow-hidden rounded-full bg-surface2">
                    <div
                      className="h-full rounded-full bg-accent transition-all duration-150"
                      style={{
                        width: `${progress.total > 0 ? (progress.done / progress.total) * 100 : 0}%`,
                      }}
                    />
                  </div>
                  <span className="text-xs text-muted-foreground">
                    {progress.done}/{progress.total}
                  </span>
                </div>
              )}

              {/* Result */}
              {result && !verifying && (
                <div
                  className={cn(
                    "mt-2 flex items-start gap-2 rounded-lg border px-3 py-2.5",
                    passed
                      ? "border-[var(--color-success)]/20 bg-[var(--color-success)]/5"
                      : "border-destructive/20 bg-destructive/5",
                  )}
                >
                  {passed ? (
                    <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-[var(--color-success)]" />
                  ) : (
                    <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
                  )}
                  <div className="text-sm">
                    {passed ? (
                      <p className="font-medium text-[var(--color-success)]">
                        Verified: {result.hashMatches} event{result.hashMatches !== 1 ? "s" : ""},
                        no modifications detected.
                        {result.skipped > 0 && (
                          <span className="font-normal text-muted-foreground">
                            {" "}({result.skipped} skipped — no parseable hash)
                          </span>
                        )}
                      </p>
                    ) : (
                      <div className="space-y-1">
                        {result.hashMismatches > 0 && (
                          <p className="font-medium text-destructive">
                            Warning: {result.hashMismatches} event{result.hashMismatches !== 1 ? "s" : ""}{" "}
                            failed hash verification.
                          </p>
                        )}
                        {result.orderViolations > 0 && (
                          <p className="font-medium text-destructive">
                            Warning: {result.orderViolations} event{result.orderViolations !== 1 ? "s" : ""}{" "}
                            out of chronological order.
                          </p>
                        )}
                        {result.hashMatches > 0 && (
                          <p className="text-muted-foreground">
                            {result.hashMatches} event{result.hashMatches !== 1 ? "s" : ""} verified
                            successfully.
                          </p>
                        )}
                      </div>
                    )}
                    {result.newestDate && result.oldestDate && (
                      <p className="mt-1 text-xs text-muted-foreground">
                        Chain:{" "}
                        <span className="font-mono">
                          {new Date(result.oldestDate).toLocaleDateString()}
                        </span>
                        {" \u2192 "}
                        <span className="font-mono">
                          {new Date(result.newestDate).toLocaleDateString()}
                        </span>
                      </p>
                    )}
                  </div>
                </div>
              )}
            </div>
          </div>
        </Card>
      )}
    </div>
  );
}
