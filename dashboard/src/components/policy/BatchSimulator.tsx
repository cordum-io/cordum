import { useState, useCallback, useRef } from "react";
import { Play, AlertCircle } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Textarea } from "../ui/Textarea";
import { useSimulatePolicy } from "../../hooks/usePolicies";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface BatchResult {
  index: number;
  topic: string;
  decision: string;
  matchedRule: string;
  evalTimeMs: number;
  error?: string;
}

const decisionBadge: Record<string, "success" | "danger" | "warning" | "info" | "default"> = {
  allow: "success",
  deny: "danger",
  require_approval: "warning",
  throttle: "info",
};

const MAX_PAYLOADS = 50;

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

interface BatchSimulatorProps {
  bundleId: string;
}

export function BatchSimulator({ bundleId }: BatchSimulatorProps) {
  const [jsonInput, setJsonInput] = useState("");
  const [parseError, setParseError] = useState("");
  const [results, setResults] = useState<BatchResult[]>([]);
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState({ completed: 0, total: 0 });
  const abortRef = useRef(false);

  const simulate = useSimulatePolicy();

  const handleRun = useCallback(async () => {
    setParseError("");
    setResults([]);
    abortRef.current = false;

    // Parse JSON
    let payloads: Record<string, unknown>[];
    try {
      const parsed = JSON.parse(jsonInput);
      if (!Array.isArray(parsed)) {
        setParseError("Input must be a JSON array of objects.");
        return;
      }
      payloads = parsed;
    } catch {
      setParseError("Invalid JSON. Please paste a valid JSON array.");
      return;
    }

    if (payloads.length === 0) {
      setParseError("Array is empty. Add at least one payload.");
      return;
    }

    if (payloads.length > MAX_PAYLOADS) {
      setParseError(`Maximum ${MAX_PAYLOADS} payloads per batch. Got ${payloads.length}.`);
      return;
    }

    setRunning(true);
    setProgress({ completed: 0, total: payloads.length });
    const batchResults: BatchResult[] = [];

    for (let i = 0; i < payloads.length; i++) {
      if (abortRef.current) break;
      const payload = payloads[i];
      const topic = typeof payload.topic === "string" ? payload.topic : `payload-${i + 1}`;

      try {
        const res = await new Promise<BatchResult>((resolve, reject) => {
          simulate.mutate(
            {
              bundleId,
              request: payload,
            },
            {
              onSuccess: (data) => {
                resolve({
                  index: i + 1,
                  topic,
                  decision: data.decision,
                  matchedRule: data.matchedRule ?? "",
                  evalTimeMs: data.evaluationTimeMs ?? 0,
                });
              },
              onError: (err) => {
                reject(err);
              },
            },
          );
        });
        batchResults.push(res);
      } catch (err) {
        batchResults.push({
          index: i + 1,
          topic,
          decision: "error",
          matchedRule: "",
          evalTimeMs: 0,
          error: err instanceof Error ? err.message : "Unknown error",
        });
      }

      setProgress({ completed: i + 1, total: payloads.length });
      setResults([...batchResults]);
    }

    setRunning(false);
  }, [jsonInput, bundleId, simulate]);

  const handleStop = useCallback(() => {
    abortRef.current = true;
  }, []);

  // Summary counts
  const summary = results.reduce(
    (acc, r) => {
      if (r.decision in acc) acc[r.decision as keyof typeof acc]++;
      return acc;
    },
    { allow: 0, deny: 0, require_approval: 0, throttle: 0, error: 0 } as Record<string, number>,
  );

  return (
    <div className="space-y-6">
      <Card>
        <div className="space-y-4">
          <h3 className="font-display text-lg font-semibold text-ink">
            Batch Simulation
          </h3>
          <p className="text-xs text-muted">
            Paste a JSON array of job payloads to simulate each one. Max {MAX_PAYLOADS} payloads.
          </p>

          <Textarea
            rows={8}
            value={jsonInput}
            onChange={(e) => setJsonInput(e.target.value)}
            placeholder={`[\n  { "topic": "job.example", "meta": { "capability": "shell", "risk_tags": ["destructive"] } },\n  { "topic": "job.safe", "meta": { "capability": "read" } }\n]`}
            className="font-mono text-xs"
          />

          {parseError && (
            <div className="flex items-center gap-1.5 text-xs text-danger">
              <AlertCircle className="h-3.5 w-3.5" />
              {parseError}
            </div>
          )}

          <div className="flex items-center gap-3">
            <Button onClick={handleRun} disabled={running || !jsonInput.trim()}>
              <Play className="h-4 w-4" />
              {running ? "Running..." : "Run Batch"}
            </Button>
            {running && (
              <Button variant="ghost" size="sm" onClick={handleStop}>
                Stop
              </Button>
            )}
          </div>

          {/* Progress bar */}
          {progress.total > 0 && (
            <div className="space-y-1">
              <div className="h-2 w-full overflow-hidden rounded-full bg-surface2">
                <div
                  className="h-full rounded-full bg-accent transition-all"
                  style={{ width: `${(progress.completed / progress.total) * 100}%` }}
                />
              </div>
              <p className="text-xs text-muted">
                {progress.completed}/{progress.total} simulated
              </p>
            </div>
          )}
        </div>
      </Card>

      {/* Summary */}
      {results.length > 0 && (
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
          {(["allow", "deny", "require_approval", "throttle", "error"] as const).map((key) => {
            const colors: Record<string, string> = {
              allow: "bg-success/10 text-success",
              deny: "bg-danger/10 text-danger",
              require_approval: "bg-warning/10 text-warning",
              throttle: "bg-accent/10 text-accent",
              error: "bg-muted/10 text-muted",
            };
            const labels: Record<string, string> = {
              allow: "Allow",
              deny: "Deny",
              require_approval: "Approval",
              throttle: "Throttle",
              error: "Error",
            };
            return (
              <div key={key} className={`rounded-xl px-3 py-2.5 text-center ${colors[key]}`}>
                <p className="text-lg font-bold">{summary[key] ?? 0}</p>
                <p className="text-[11px] font-medium">{labels[key]}</p>
              </div>
            );
          })}
        </div>
      )}

      {/* Results table */}
      {results.length > 0 && (
        <Card>
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-border text-left text-muted">
                  <th className="px-3 py-2 font-semibold">#</th>
                  <th className="px-3 py-2 font-semibold">Topic</th>
                  <th className="px-3 py-2 font-semibold">Decision</th>
                  <th className="px-3 py-2 font-semibold">Matched Rule</th>
                  <th className="px-3 py-2 font-semibold text-right">Eval (ms)</th>
                </tr>
              </thead>
              <tbody>
                {results.map((r) => (
                  <tr
                    key={r.index}
                    className="border-b border-border/50 transition hover:bg-surface2/30"
                  >
                    <td className="px-3 py-2 font-mono text-muted">{r.index}</td>
                    <td className="px-3 py-2 font-medium text-ink">{r.topic}</td>
                    <td className="px-3 py-2">
                      {r.error ? (
                        <Badge variant="default">error</Badge>
                      ) : (
                        <Badge variant={decisionBadge[r.decision] ?? "default"}>
                          {r.decision}
                        </Badge>
                      )}
                    </td>
                    <td className="px-3 py-2 font-mono text-muted">
                      {r.error ? (
                        <span className="text-danger">{r.error}</span>
                      ) : (
                        r.matchedRule || "\u2014"
                      )}
                    </td>
                    <td className="px-3 py-2 text-right text-muted">
                      {r.error ? "\u2014" : r.evalTimeMs}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
    </div>
  );
}
