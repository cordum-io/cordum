// ToolUsageHeatmap — agents × tools matrix of MCP invocation counts.
//
// Design intent: a dense, monospace-leaning grid that reads like the
// audit it represents. The cell ramp encodes deny-rate (green when
// allow-dominant, amber as deny-share climbs, red when more than half
// the calls were denied) so the operator sees policy hot-spots at a
// glance. Every cell carries a numeric label AND a verbose aria-label
// — colour is never the only signal.
//
// Mobile (<768px): the matrix collapses into a list grouped by agent,
// rendered with the same row component so the keyboard / a11y story
// stays identical between layouts.
import { useEffect, useMemo, useRef, useState } from "react";
import type { MCPUsageCell } from "../../api/types";
import { cn } from "../../lib/utils";

export interface ToolUsageHeatmapProps {
  cells: MCPUsageCell[];
  onCellClick?: (cell: MCPUsageCell) => void;
  className?: string;
}

interface MatrixModel {
  agents: string[];
  tools: string[];
  byKey: Map<string, MCPUsageCell>;
  maxCount: number;
}

function buildMatrix(cells: MCPUsageCell[]): MatrixModel {
  const agents = new Set<string>();
  const tools = new Set<string>();
  const byKey = new Map<string, MCPUsageCell>();
  let maxCount = 0;
  for (const c of cells) {
    agents.add(c.agent_id);
    tools.add(c.tool_name);
    byKey.set(`${c.agent_id}::${c.tool_name}`, c);
    if (c.count > maxCount) maxCount = c.count;
  }
  return {
    agents: [...agents].sort(),
    tools: [...tools].sort(),
    byKey,
    maxCount,
  };
}

// classify returns {label, classes} for a cell. The colour ramp lives
// here so the legend + cells stay in sync. Tokens reference CSS
// custom properties from globals.css so dark-mode is automatic.
function classify(cell: MCPUsageCell | undefined): { variant: string; classes: string } {
  if (!cell || cell.count === 0) {
    return {
      variant: "empty",
      classes: "bg-[color:var(--surface-subtle,rgba(120,120,120,0.05))] text-[color:var(--text-muted,#888)]",
    };
  }
  const total = cell.allow_count + cell.deny_count + cell.approval_required_count;
  const denyShare = total > 0 ? cell.deny_count / total : 0;
  if (denyShare >= 0.5) {
    return {
      variant: "deny-dominant",
      classes:
        "bg-[color:var(--accent-danger,#7f1d1d)]/30 text-[color:var(--accent-danger,#fca5a5)] ring-1 ring-[color:var(--accent-danger,#fca5a5)]/40",
    };
  }
  if (denyShare >= 0.15 || cell.approval_required_count > 0) {
    return {
      variant: "mixed",
      classes:
        "bg-amber-500/20 text-amber-700 dark:text-amber-300 ring-1 ring-amber-500/40",
    };
  }
  return {
    variant: "allow-dominant",
    classes:
      "bg-emerald-500/20 text-emerald-700 dark:text-emerald-300 ring-1 ring-emerald-500/30",
  };
}

function ariaLabelFor(cell: MCPUsageCell | undefined, agent: string, tool: string): string {
  if (!cell || cell.count === 0) {
    return `agent ${agent} tool ${tool}: no calls`;
  }
  const total = cell.allow_count + cell.deny_count + cell.approval_required_count;
  const allowPct = total > 0 ? Math.round((cell.allow_count * 100) / total) : 0;
  const denyPct = total > 0 ? Math.round((cell.deny_count * 100) / total) : 0;
  return `agent ${agent} tool ${tool}: ${cell.count} calls, ${allowPct}% allow, ${denyPct}% deny`;
}

function useIsCompactLayout(): boolean {
  const [compact, setCompact] = useState(() => {
    if (typeof window === "undefined") return false;
    return window.matchMedia("(max-width: 767px)").matches;
  });
  useEffect(() => {
    if (typeof window === "undefined") return;
    const mql = window.matchMedia("(max-width: 767px)");
    const handler = (ev: MediaQueryListEvent) => setCompact(ev.matches);
    if (typeof mql.addEventListener === "function") {
      mql.addEventListener("change", handler);
      return () => mql.removeEventListener("change", handler);
    }
    mql.addListener(handler);
    return () => mql.removeListener(handler);
  }, []);
  return compact;
}

export function ToolUsageHeatmap(props: ToolUsageHeatmapProps) {
  const { cells, onCellClick, className } = props;
  const matrix = useMemo(() => buildMatrix(cells), [cells]);
  const compact = useIsCompactLayout();
  const [activeKey, setActiveKey] = useState<string | null>(null);

  if (matrix.agents.length === 0) {
    return (
      <div
        data-testid="tool-usage-heatmap-empty"
        className={cn(
          "rounded-xl border border-dashed border-[color:var(--border-color,#27272a)] bg-[color:var(--surface,#0b0b0e)] p-8 text-center text-sm text-[color:var(--text-muted,#a1a1aa)]",
          className,
        )}
      >
        No MCP tool activity in the selected window.
      </div>
    );
  }

  const activeCell = activeKey ? matrix.byKey.get(activeKey) : undefined;

  return (
    <div className={cn("flex flex-col gap-4", className)} data-testid="tool-usage-heatmap">
      {compact ? (
        <CompactList
          matrix={matrix}
          onActivate={(c) => {
            setActiveKey(`${c.agent_id}::${c.tool_name}`);
            onCellClick?.(c);
          }}
        />
      ) : (
        <Grid
          matrix={matrix}
          onActivate={(c) => {
            setActiveKey(`${c.agent_id}::${c.tool_name}`);
            onCellClick?.(c);
          }}
          activeKey={activeKey}
        />
      )}

      <Legend />
      {activeCell && <CellDetail cell={activeCell} onClose={() => setActiveKey(null)} />}
    </div>
  );
}

function Legend() {
  return (
    <ul
      className="flex flex-wrap items-center gap-3 text-xs text-[color:var(--text-muted,#a1a1aa)]"
      data-testid="tool-usage-heatmap-legend"
      aria-label="Heatmap legend"
    >
      <Swatch className="bg-emerald-500/30" label="Allow dominant" />
      <Swatch className="bg-amber-500/30" label="Mixed / approval" />
      <Swatch className="bg-[color:var(--accent-danger,#7f1d1d)]/40" label="Deny dominant" />
      <Swatch className="bg-[color:var(--surface-subtle,rgba(120,120,120,0.1))]" label="No calls" />
    </ul>
  );
}

function Swatch({ className, label }: { className: string; label: string }) {
  return (
    <li className="flex items-center gap-1.5">
      <span
        aria-hidden="true"
        className={cn("inline-block h-3 w-3 rounded-sm", className)}
      />
      <span>{label}</span>
    </li>
  );
}

interface GridProps {
  matrix: MatrixModel;
  onActivate: (cell: MCPUsageCell) => void;
  activeKey: string | null;
}

function Grid({ matrix, onActivate, activeKey }: GridProps) {
  const tableRef = useRef<HTMLTableElement>(null);
  return (
    <div className="overflow-x-auto">
      <table
        ref={tableRef}
        className="min-w-full border-separate border-spacing-1 text-xs"
        role="grid"
        aria-label="MCP tool usage by agent"
      >
        <thead>
          <tr>
            <th
              scope="col"
              className="sticky left-0 z-10 bg-[color:var(--surface,#0b0b0e)] px-2 py-1 text-left font-mono text-[10px] uppercase tracking-wider text-[color:var(--text-muted,#a1a1aa)]"
            >
              agent ╲ tool
            </th>
            {matrix.tools.map((tool) => (
              <th
                key={tool}
                scope="col"
                className="px-1 py-1 text-left font-mono text-[10px] text-[color:var(--text,#e4e4e7)]"
                title={tool}
              >
                {tool}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {matrix.agents.map((agent) => (
            <tr key={agent}>
              <th
                scope="row"
                className="sticky left-0 z-10 bg-[color:var(--surface,#0b0b0e)] px-2 py-1 text-left font-mono text-[11px] text-[color:var(--text,#e4e4e7)]"
              >
                {agent}
              </th>
              {matrix.tools.map((tool) => {
                const key = `${agent}::${tool}`;
                const cell = matrix.byKey.get(key);
                const cls = classify(cell);
                const isActive = activeKey === key;
                return (
                  <td key={key} className="p-0">
                    <button
                      type="button"
                      data-testid={`heatmap-cell-${agent}-${tool}`}
                      data-variant={cls.variant}
                      className={cn(
                        "h-9 w-12 rounded-sm font-mono text-[11px] tabular-nums transition-colors duration-150 hover:brightness-125 focus:outline-none focus-visible:ring-2 focus-visible:ring-[color:var(--accent,#a78bfa)]",
                        cls.classes,
                        isActive && "ring-2 ring-offset-1 ring-offset-[color:var(--surface,#0b0b0e)]",
                      )}
                      aria-label={ariaLabelFor(cell, agent, tool)}
                      aria-pressed={isActive}
                      disabled={!cell || cell.count === 0}
                      onClick={() => cell && onActivate(cell)}
                      onKeyDown={(e) => handleGridKeydown(e, tableRef.current, agent, matrix)}
                    >
                      {cell ? cell.count : ""}
                    </button>
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// handleGridKeydown moves focus across the grid in response to arrow
// keys. Falls back to default scrolling if the next cell is missing
// (sparse grids would otherwise trap the keyboard user).
function handleGridKeydown(
  e: React.KeyboardEvent<HTMLButtonElement>,
  table: HTMLTableElement | null,
  agent: string,
  matrix: MatrixModel,
) {
  if (!table) return;
  const moves: Record<string, [number, number]> = {
    ArrowUp: [-1, 0],
    ArrowDown: [1, 0],
    ArrowLeft: [0, -1],
    ArrowRight: [0, 1],
  };
  const move = moves[e.key];
  if (!move) return;
  e.preventDefault();
  const tools = matrix.tools;
  const agents = matrix.agents;
  const currentTool = (e.currentTarget.getAttribute("data-testid") || "")
    .split("-")
    .slice(-1)[0];
  const agentIdx = agents.indexOf(agent);
  const toolIdx = tools.indexOf(currentTool);
  const nextAgent = agents[Math.max(0, Math.min(agents.length - 1, agentIdx + move[0]))];
  const nextTool = tools[Math.max(0, Math.min(tools.length - 1, toolIdx + move[1]))];
  const target = table.querySelector<HTMLButtonElement>(
    `[data-testid="heatmap-cell-${nextAgent}-${nextTool}"]`,
  );
  target?.focus();
}

interface CompactListProps {
  matrix: MatrixModel;
  onActivate: (cell: MCPUsageCell) => void;
}

function CompactList({ matrix, onActivate }: CompactListProps) {
  return (
    <div className="flex flex-col divide-y divide-[color:var(--border-color,#27272a)]" data-testid="tool-usage-heatmap-compact">
      {matrix.agents.map((agent) => {
        const cells = matrix.tools
          .map((tool) => matrix.byKey.get(`${agent}::${tool}`))
          .filter((c): c is MCPUsageCell => Boolean(c) && (c?.count ?? 0) > 0);
        return (
          <section key={agent} aria-label={`Agent ${agent}`} className="py-2">
            <h4 className="mb-1 font-mono text-xs uppercase text-[color:var(--text-muted,#a1a1aa)]">
              {agent}
            </h4>
            {cells.length === 0 ? (
              <p className="text-xs text-[color:var(--text-muted,#a1a1aa)]">No calls in window.</p>
            ) : (
              <ul className="flex flex-col gap-1">
                {cells.map((cell) => {
                  const cls = classify(cell);
                  return (
                    <li key={cell.tool_name}>
                      <button
                        type="button"
                        className={cn(
                          "flex w-full items-center justify-between rounded-sm px-2 py-1.5 font-mono text-xs",
                          cls.classes,
                        )}
                        aria-label={ariaLabelFor(cell, agent, cell.tool_name)}
                        onClick={() => onActivate(cell)}
                        data-testid={`heatmap-row-${agent}-${cell.tool_name}`}
                      >
                        <span>{cell.tool_name}</span>
                        <span className="tabular-nums">{cell.count}</span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          </section>
        );
      })}
    </div>
  );
}

interface CellDetailProps {
  cell: MCPUsageCell;
  onClose: () => void;
}

function CellDetail({ cell, onClose }: CellDetailProps) {
  const total = cell.allow_count + cell.deny_count + cell.approval_required_count;
  const lastInvoked =
    cell.last_invoked_at_ms > 0
      ? new Date(cell.last_invoked_at_ms).toISOString()
      : "—";
  return (
    <aside
      role="region"
      aria-label="Cell details"
      data-testid="heatmap-cell-detail"
      className="rounded-xl border border-[color:var(--border-color,#27272a)] bg-[color:var(--surface-elevated,#111114)] p-3 text-xs text-[color:var(--text,#e4e4e7)]"
    >
      <header className="mb-2 flex items-center justify-between">
        <span className="font-mono">
          {cell.agent_id} <span className="text-[color:var(--text-muted,#a1a1aa)]">→</span>{" "}
          {cell.tool_name}
        </span>
        <button
          type="button"
          className="text-[color:var(--text-muted,#a1a1aa)] hover:text-[color:var(--text,#e4e4e7)]"
          onClick={onClose}
          aria-label="Close cell details"
        >
          ✕
        </button>
      </header>
      <dl className="grid grid-cols-2 gap-x-4 gap-y-1 sm:grid-cols-3">
        <Stat label="Calls" value={cell.count} />
        <Stat label="Allow" value={`${cell.allow_count} (${pct(cell.allow_count, total)}%)`} />
        <Stat label="Deny" value={`${cell.deny_count} (${pct(cell.deny_count, total)}%)`} />
        <Stat label="Approval req." value={cell.approval_required_count} />
        <Stat label="p50 latency" value={`${cell.p50_latency_ms.toFixed(0)} ms`} />
        <Stat label="p99 latency" value={`${cell.p99_latency_ms.toFixed(0)} ms`} />
      </dl>
      <p className="mt-2 text-[10px] text-[color:var(--text-muted,#a1a1aa)]">
        Last invoked: {lastInvoked}
      </p>
    </aside>
  );
}

function Stat({ label, value }: { label: string; value: number | string }) {
  return (
    <div>
      <dt className="text-[10px] uppercase tracking-wider text-[color:var(--text-muted,#a1a1aa)]">
        {label}
      </dt>
      <dd className="font-mono tabular-nums">{value}</dd>
    </div>
  );
}

function pct(n: number, total: number): string {
  if (total <= 0) return "0";
  return ((n * 100) / total).toFixed(0);
}

export default ToolUsageHeatmap;

