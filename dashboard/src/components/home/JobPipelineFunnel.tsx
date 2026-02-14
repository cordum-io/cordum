import { useNavigate } from "react-router-dom";
import { useStatus } from "../../hooks/useStatus";
import { Card } from "../ui/Card";

// ---------------------------------------------------------------------------
// Stage config
// ---------------------------------------------------------------------------

interface Stage {
  key: string;
  label: string;
  color: string;
  /** URL search param value for /jobs?state=X */
  stateParam: string;
}

const STAGES: Stage[] = [
  { key: "pending", label: "Submitted", color: "#7b8794", stateParam: "pending" },
  { key: "safety", label: "Safety Check", color: "#d97706", stateParam: "pending" },
  { key: "dispatched", label: "Dispatched", color: "#4f46e5", stateParam: "dispatched" },
  { key: "running", label: "Running", color: "#2563eb", stateParam: "running" },
  { key: "succeeded", label: "Succeeded", color: "#059669", stateParam: "succeeded" },
  { key: "failed", label: "Failed", color: "#dc2626", stateParam: "failed" },
];

function pct(part: number, total: number): number {
  if (total <= 0) return 0;
  return Math.round((part / total) * 100);
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function JobPipelineFunnel() {
  const navigate = useNavigate();
  const { data: status, isLoading } = useStatus();

  const pipeline = status?.pipeline;

  // Build chart data — safety check is estimated as submitted minus dispatched+running+succeeded+failed
  const safetyCount = pipeline
    ? Math.max(0, pipeline.pending ?? 0)
    : 0;

  const data = pipeline
    ? STAGES.map((stage) => {
      let count = 0;
      switch (stage.key) {
        case "pending":
            // Total submitted ≈ sum of all stages
            count =
              (pipeline.pending ?? 0) +
              (pipeline.dispatched ?? 0) +
              (pipeline.running ?? 0) +
              (pipeline.succeeded ?? 0) +
              (pipeline.failed ?? 0);
            break;
          case "safety":
            count = safetyCount + (pipeline.dispatched ?? 0) + (pipeline.running ?? 0);
            break;
          case "dispatched":
            count = pipeline.dispatched ?? 0;
            break;
          case "running":
            count = pipeline.running ?? 0;
            break;
          case "succeeded":
            count = pipeline.succeeded ?? 0;
            break;
          case "failed":
            count = pipeline.failed ?? 0;
            break;
        }
        return { key: stage.key, label: stage.label, count, color: stage.color, stateParam: stage.stateParam };
      })
    : [];
  const submitted = data.find((d) => d.key === "pending")?.count ?? 0;
  const inFlight = (pipeline?.pending ?? 0) + (pipeline?.dispatched ?? 0) + (pipeline?.running ?? 0);
  const terminalTotal = (pipeline?.succeeded ?? 0) + (pipeline?.failed ?? 0);
  const successRate = pct(pipeline?.succeeded ?? 0, terminalTotal);
  const maxStageCount = Math.max(1, ...data.map((d) => d.count));

  if (isLoading) {
    return (
      <Card className="flex h-[430px] min-h-[430px] flex-col">
        <div className="space-y-3">
          <div className="h-4 w-1/3 rounded bg-surface2 animate-pulse" />
          <div className="h-48 rounded bg-surface2 animate-pulse" />
        </div>
      </Card>
    );
  }

  if (!pipeline) {
    return (
      <Card className="flex h-[430px] min-h-[430px] flex-col">
        <div className="space-y-3">
          <h3 className="text-sm font-semibold text-ink">Job Pipeline</h3>
          <p className="text-xs text-muted">
            Pipeline metrics are not available from the gateway.
          </p>
        </div>
      </Card>
    );
  }

  return (
    <Card className="flex h-[430px] min-h-[430px] flex-col">
      <div className="flex min-h-0 flex-1 flex-col gap-4">
        <div className="flex items-start justify-between">
          <div>
            <h3 className="text-sm font-semibold text-ink">Job Pipeline</h3>
            <p className="text-[11px] text-muted">Live execution flow by stage</p>
          </div>
          <span className="rounded-full border border-border bg-surface2 px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wide text-muted">
            realtime
          </span>
        </div>

        <div className="grid grid-cols-3 gap-2">
          <div className="rounded-xl border border-border/70 bg-surface2/40 px-3 py-2">
            <p className="text-[10px] uppercase tracking-wide text-muted">Submitted</p>
            <p className="mt-1 text-sm font-semibold text-ink">{submitted.toLocaleString()}</p>
          </div>
          <div className="rounded-xl border border-border/70 bg-surface2/40 px-3 py-2">
            <p className="text-[10px] uppercase tracking-wide text-muted">In Flight</p>
            <p className="mt-1 text-sm font-semibold text-ink">{inFlight.toLocaleString()}</p>
          </div>
          <div className="rounded-xl border border-border/70 bg-surface2/40 px-3 py-2">
            <p className="text-[10px] uppercase tracking-wide text-muted">Success Rate</p>
            <p className="mt-1 text-sm font-semibold text-ink">{successRate}%</p>
          </div>
        </div>

        <div className="min-h-0 flex-1 space-y-2 overflow-y-auto pr-1">
          {data.map((stage) => {
            const widthPct = pct(stage.count, maxStageCount);
            const totalPct = pct(stage.count, submitted);
            return (
              <button
                key={stage.key}
                type="button"
                className="w-full rounded-xl border border-border/80 bg-surface2/30 px-3 py-2 text-left transition-colors hover:bg-surface2/50"
                onClick={() => navigate(`/jobs?state=${stage.stateParam}`)}
              >
                <div className="flex items-center justify-between text-xs">
                  <div className="flex items-center gap-2">
                    <span
                      className="h-2.5 w-2.5 shrink-0 rounded-full"
                      style={{ backgroundColor: stage.color }}
                    />
                    <span className="font-medium text-ink">{stage.label}</span>
                  </div>
                  <span className="font-mono text-muted">{stage.count.toLocaleString()}</span>
                </div>
                <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-[color:rgba(90,106,112,0.15)]">
                  <div
                    className="h-full rounded-full transition-all duration-500 ease-[cubic-bezier(0.16,1,0.3,1)]"
                    style={{ width: `${widthPct}%`, backgroundColor: stage.color }}
                  />
                </div>
                <div className="mt-1 flex items-center justify-between text-[10px] text-muted">
                  <span>{totalPct}% of submitted</span>
                  <span>{widthPct}% of peak</span>
                </div>
              </button>
            );
          })}
        </div>
      </div>
    </Card>
  );
}
