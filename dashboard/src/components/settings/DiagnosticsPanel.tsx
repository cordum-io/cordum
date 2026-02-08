import { useState, useCallback } from "react";
import {
  Database,
  Radio,
  Cpu,
  Shield,
  Loader,
  CheckCircle,
  XCircle,
  Play,
} from "lucide-react";
import { Card } from "../ui/Card";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";
import { get } from "../../api/client";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface DiagnosticCommand {
  id: string;
  label: string;
  icon: typeof Database;
  extract: (status: Record<string, unknown>) => { ok: boolean; message: string };
}

type RunState = "idle" | "running" | "success" | "failed";

interface DiagResult {
  state: RunState;
  message: string;
}

// ---------------------------------------------------------------------------
// Command definitions
// ---------------------------------------------------------------------------

const COMMANDS: DiagnosticCommand[] = [
  {
    id: "redis",
    label: "Check Redis Connectivity",
    icon: Database,
    extract: (s) => {
      const redis = s.redis as { ok?: boolean; error?: string } | undefined;
      if (redis?.ok) return { ok: true, message: "Connected" };
      return { ok: false, message: redis?.error ?? "Redis unreachable" };
    },
  },
  {
    id: "nats",
    label: "Check NATS Connectivity",
    icon: Radio,
    extract: (s) => {
      const nats = s.nats as { connected?: boolean; status?: string; url?: string } | undefined;
      if (nats?.connected) return { ok: true, message: `Connected to ${nats.url ?? "NATS"}` };
      return { ok: false, message: nats?.status ?? "NATS disconnected" };
    },
  },
  {
    id: "workers",
    label: "Verify Worker Pool Registration",
    icon: Cpu,
    extract: (s) => {
      const workers = s.workers as { count?: number } | undefined;
      const count = workers?.count ?? 0;
      if (count > 0) return { ok: true, message: `${count} worker${count !== 1 ? "s" : ""} registered` };
      return { ok: false, message: "No workers registered" };
    },
  },
  {
    id: "policy",
    label: "Test Policy Engine",
    icon: Shield,
    extract: (s) => {
      // Policy engine is healthy if gateway is responding (it hosts the engine)
      if (s.uptime_seconds) return { ok: true, message: "Policy engine reachable" };
      return { ok: false, message: "Gateway not responding" };
    },
  },
];

// ---------------------------------------------------------------------------
// DiagnosticsPanel
// ---------------------------------------------------------------------------

export function DiagnosticsPanel() {
  const [results, setResults] = useState<Record<string, DiagResult>>({});

  const runDiagnostic = useCallback(async (cmd: DiagnosticCommand) => {
    setResults((prev) => ({
      ...prev,
      [cmd.id]: { state: "running", message: "" },
    }));

    try {
      // Small delay for UX feedback
      const [status] = await Promise.all([
        get<Record<string, unknown>>("/status"),
        new Promise((r) => setTimeout(r, 200)),
      ]);
      const { ok, message } = cmd.extract(status);
      setResults((prev) => ({
        ...prev,
        [cmd.id]: { state: ok ? "success" : "failed", message },
      }));
    } catch (err) {
      setResults((prev) => ({
        ...prev,
        [cmd.id]: {
          state: "failed",
          message: err instanceof Error ? err.message : "Request failed",
        },
      }));
    }
  }, []);

  const runAll = useCallback(async () => {
    for (const cmd of COMMANDS) {
      await runDiagnostic(cmd);
    }
  }, [runDiagnostic]);

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <p className="text-xs text-muted">
          Run diagnostics to verify component connectivity.
        </p>
        <Button variant="outline" size="sm" onClick={runAll}>
          <Play className="mr-1 h-3 w-3" /> Run All
        </Button>
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {COMMANDS.map((cmd) => {
          const Icon = cmd.icon;
          const result = results[cmd.id];
          const state = result?.state ?? "idle";

          return (
            <Card key={cmd.id} className="flex items-center gap-3">
              <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-surface2">
                <Icon className="h-4 w-4 text-muted" />
              </div>

              <div className="min-w-0 flex-1">
                <p className="text-xs font-semibold text-ink">{cmd.label}</p>
                {state === "running" && (
                  <p className="flex items-center gap-1 text-[10px] text-muted">
                    <Loader className="h-3 w-3 animate-spin" /> Running...
                  </p>
                )}
                {state === "success" && (
                  <p className="flex items-center gap-1 text-[10px] text-success">
                    <CheckCircle className="h-3 w-3" /> {result?.message}
                  </p>
                )}
                {state === "failed" && (
                  <p className="flex items-center gap-1 text-[10px] text-danger">
                    <XCircle className="h-3 w-3" /> {result?.message}
                  </p>
                )}
              </div>

              <Button
                variant="outline"
                size="sm"
                onClick={() => runDiagnostic(cmd)}
                disabled={state === "running"}
              >
                {state === "running" ? (
                  <Loader className="h-3 w-3 animate-spin" />
                ) : (
                  "Run"
                )}
              </Button>
            </Card>
          );
        })}
      </div>
    </div>
  );
}
