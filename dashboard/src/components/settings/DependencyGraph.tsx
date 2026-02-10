import { Card } from "../ui/Card";
import { cn } from "../../lib/utils";
import type { ComponentHealth } from "./SystemHealthTab";

// ---------------------------------------------------------------------------
// Node positions (500x300 viewBox)
// ---------------------------------------------------------------------------

interface NodeDef {
  name: string;
  x: number;
  y: number;
}

const NODES: NodeDef[] = [
  { name: "Gateway", x: 250, y: 50 },
  { name: "Redis", x: 100, y: 230 },
  { name: "NATS", x: 400, y: 230 },
  { name: "Workers", x: 250, y: 230 },
];

// Edges: [from, to]
const EDGES: [string, string][] = [
  ["Gateway", "Redis"],
  ["Gateway", "NATS"],
  ["Workers", "NATS"],
  ["Workers", "Redis"],
];

// ---------------------------------------------------------------------------
// Status colors
// ---------------------------------------------------------------------------

const STATUS_FILL: Record<string, string> = {
  healthy: "#10b981",
  degraded: "#f59e0b",
  down: "#ef4444",
};

const STATUS_NODE_STROKE: Record<string, string> = {
  healthy: "#d1fae5",
  degraded: "#fef3c7",
  down: "#fee2e2",
};

function edgeColor(a: string, b: string): string {
  if (a === "down" || b === "down") return "#ef4444";
  if (a === "degraded" || b === "degraded") return "#f59e0b";
  return "#10b981";
}

// ---------------------------------------------------------------------------
// DependencyGraph
// ---------------------------------------------------------------------------

interface DependencyGraphProps {
  components: ComponentHealth[];
}

export function DependencyGraph({ components }: DependencyGraphProps) {
  const statusMap = new Map(components.map((c) => [c.name, c.status]));

  const getStatus = (name: string) => statusMap.get(name) ?? "down";

  const nodeMap = new Map(NODES.map((n) => [n.name, n]));

  return (
    <Card>
      <p className="mb-2 text-xs font-semibold text-muted uppercase tracking-wide">
        Dependency Graph
      </p>
      <svg
        viewBox="0 0 500 300"
        className="w-full max-w-lg mx-auto"
        aria-label="System dependency graph"
      >
        {/* Edges */}
        {EDGES.map(([from, to]) => {
          const a = nodeMap.get(from);
          const b = nodeMap.get(to);
          if (!a || !b) return null;
          const color = edgeColor(getStatus(from), getStatus(to));
          const isUnhealthy = getStatus(from) !== "healthy" || getStatus(to) !== "healthy";
          return (
            <line
              key={`${from}-${to}`}
              x1={a.x}
              y1={a.y + 20}
              x2={b.x}
              y2={b.y - 20}
              stroke={color}
              strokeWidth={2}
              strokeDasharray={isUnhealthy ? "6 4" : undefined}
              opacity={0.7}
            >
              {isUnhealthy && (
                <animate
                  attributeName="stroke-dashoffset"
                  from="0"
                  to="20"
                  dur="1s"
                  repeatCount="indefinite"
                />
              )}
            </line>
          );
        })}

        {/* Nodes */}
        {NODES.map((node) => {
          const status = getStatus(node.name);
          return (
            <g key={node.name}>
              {/* Node background */}
              <rect
                x={node.x - 50}
                y={node.y - 18}
                width={100}
                height={36}
                rx={8}
                fill={STATUS_NODE_STROKE[status]}
                stroke={STATUS_FILL[status]}
                strokeWidth={1.5}
              />
              {/* Status dot */}
              <circle
                cx={node.x - 35}
                cy={node.y}
                r={5}
                fill={STATUS_FILL[status]}
              >
                {status === "down" && (
                  <animate
                    attributeName="opacity"
                    values="1;0.3;1"
                    dur="1.5s"
                    repeatCount="indefinite"
                  />
                )}
              </circle>
              {/* Label */}
              <text
                x={node.x + 5}
                y={node.y + 4}
                textAnchor="middle"
                className="text-[12px] font-medium"
                fill="#374151"
              >
                {node.name}
              </text>
            </g>
          );
        })}
      </svg>
    </Card>
  );
}
