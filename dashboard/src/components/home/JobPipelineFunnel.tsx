import { useNavigate } from "react-router-dom";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  Cell,
} from "recharts";
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
  { key: "pending", label: "Submitted", color: "#8b8fa3", stateParam: "pending" },
  { key: "safety", label: "Safety Check", color: "#f59e0b", stateParam: "pending" },
  { key: "dispatched", label: "Dispatched", color: "#6366f1", stateParam: "dispatched" },
  { key: "running", label: "Running", color: "#3b82f6", stateParam: "running" },
  { key: "succeeded", label: "Succeeded", color: "#10b981", stateParam: "succeeded" },
  { key: "failed", label: "Failed", color: "#ef4444", stateParam: "failed" },
];

// ---------------------------------------------------------------------------
// Custom tooltip
// ---------------------------------------------------------------------------

function PipelineTooltip({ active, payload }: { active?: boolean; payload?: Array<{ payload: { label: string; count: number } }> }) {
  if (!active || !payload?.length) return null;
  const d = payload[0].payload;
  return (
    <div className="rounded-lg border border-border bg-surface px-3 py-2 shadow-lg">
      <p className="text-xs font-medium text-ink">{d.label}</p>
      <p className="text-sm font-bold text-ink">{d.count.toLocaleString()}</p>
    </div>
  );
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

  if (isLoading) {
    return (
      <Card>
        <div className="space-y-3">
          <div className="h-4 w-1/3 rounded bg-surface2 animate-pulse" />
          <div className="h-48 rounded bg-surface2 animate-pulse" />
        </div>
      </Card>
    );
  }

  if (!pipeline) {
    return (
      <Card>
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
    <Card>
      <div className="space-y-3">
        <h3 className="text-sm font-semibold text-ink">Job Pipeline</h3>
        <div className="h-56">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart
              data={data}
              layout="vertical"
              margin={{ top: 0, right: 16, bottom: 0, left: 0 }}
            >
              <XAxis type="number" hide />
              <YAxis
                type="category"
                dataKey="label"
                width={100}
                tick={{ fontSize: 12, fill: "var(--color-muted)" }}
                axisLine={false}
                tickLine={false}
              />
              <Tooltip
                content={<PipelineTooltip />}
                cursor={{ fill: "rgba(0,0,0,0.04)" }}
              />
              <Bar
                dataKey="count"
                radius={[0, 6, 6, 0]}
                cursor="pointer"
                onClick={(entry: { stateParam?: string }) => {
                  if (entry?.stateParam) {
                    navigate(`/jobs?state=${entry.stateParam}`);
                  }
                }}
              >
                {data.map((d) => (
                  <Cell key={d.key} fill={d.color} />
                ))}
              </Bar>
            </BarChart>
          </ResponsiveContainer>
        </div>
      </div>
    </Card>
  );
}
