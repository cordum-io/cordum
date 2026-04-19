// MCPPage — single-screen view of MCP governance: usage heatmap,
// approval queue, outbound call log. Each section is its own widget
// reading from the shared MCP hooks; the page composes them and owns
// only the surrounding filters (time range + status tabs).
//
// Admin-gated by the route wrapper (RequireRole) so the menu entry
// surfaces for everyone but the page returns 403 fallback for
// non-admins.
import { useMemo, useState } from "react";
import { PageHeader } from "@/components/layout/PageHeader";
import { ToolUsageHeatmap } from "@/components/mcp/ToolUsageHeatmap";
import {
  MCPApprovalQueue,
  type MCPApprovalQueueStatus,
} from "@/components/mcp/MCPApprovalQueue";
import { MCPOutboundLog } from "@/components/mcp/MCPOutboundLog";
import { PromptCatalog } from "@/components/mcp/PromptCatalog";
import { useMcpUsage, useMcpPendingApprovals } from "@/hooks/useMcp";
import { useMcpPrompts } from "@/hooks/useMcpCatalog";
import type { SignatureStatus } from "@/api/types";

const APPROVAL_STATUSES: MCPApprovalQueueStatus[] = [
  "pending",
  "approved",
  "rejected",
  "expired",
];

const RANGE_PRESETS: Record<string, number> = {
  "1h": 60 * 60 * 1000,
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
  "30d": 30 * 24 * 60 * 60 * 1000,
};

const SIG_FILTERS: Array<SignatureStatus | "all"> = [
  "all",
  "verified",
  "unverified",
  "invalid",
];

export function MCPPage() {
  const [rangeKey, setRangeKey] = useState<keyof typeof RANGE_PRESETS>("24h");
  const [agentFilter, setAgentFilter] = useState("");
  const [toolFilter, setToolFilter] = useState("");
  const [serverFilter, setServerFilter] = useState("");
  const [sigFilter, setSigFilter] = useState<SignatureStatus | "all">("all");
  const [approvalStatus, setApprovalStatus] = useState<MCPApprovalQueueStatus>("pending");

  const window = useMemo(() => {
    const untilMs = Date.now();
    const sinceMs = untilMs - RANGE_PRESETS[rangeKey];
    return { sinceMs, untilMs };
  }, [rangeKey]);

  const usage = useMcpUsage({
    sinceMs: window.sinceMs,
    untilMs: window.untilMs,
    agent: agentFilter || undefined,
    tool: toolFilter || undefined,
  });
  const pending = useMcpPendingApprovals("pending");

  const denyTotals = useMemo(() => {
    if (!usage.data) return { deny: 0, approvalRequired: 0 };
    let deny = 0;
    let approvalRequired = 0;
    for (const c of usage.data.cells) {
      deny += c.deny_count;
      approvalRequired += c.approval_required_count;
    }
    return { deny, approvalRequired };
  }, [usage.data]);

  return (
    <div className="flex flex-col gap-6" data-testid="mcp-page">
      <PageHeader
        title="MCP governance"
        subtitle="Inbound tool calls + outbound signed requests, scoped to the active tenant."
      />

      <section
        aria-label="Summary"
        className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4"
        data-testid="mcp-summary"
      >
        <Stat label="Tool calls" value={usage.data?.total_calls ?? 0} hint={`Last ${rangeKey}`} />
        <Stat
          label="Pending approvals"
          value={pending.data?.length ?? 0}
          tone={(pending.data?.length ?? 0) > 0 ? "warn" : "ok"}
        />
        <Stat label="Denied calls" value={denyTotals.deny} tone={denyTotals.deny > 0 ? "warn" : "ok"} />
        <Stat label="Approval required" value={denyTotals.approvalRequired} />
      </section>

      <section aria-label="Time range and filters" className="flex flex-wrap items-end gap-3" data-testid="mcp-filters">
        <FieldLabel label="Window">
          <div className="inline-flex rounded border border-[color:var(--border-color,#27272a)] bg-[color:var(--surface,#0b0b0e)]" role="group" aria-label="Time window">
            {Object.keys(RANGE_PRESETS).map((k) => (
              <button
                key={k}
                type="button"
                aria-pressed={rangeKey === k}
                onClick={() => setRangeKey(k as keyof typeof RANGE_PRESETS)}
                className={`px-3 py-1 text-xs font-mono ${
                  rangeKey === k
                    ? "bg-[color:var(--accent,#a78bfa)]/20 text-[color:var(--accent,#a78bfa)]"
                    : "text-[color:var(--text-muted,#a1a1aa)]"
                }`}
                data-testid={`mcp-range-${k}`}
              >
                {k}
              </button>
            ))}
          </div>
        </FieldLabel>
        <FieldLabel label="Agent">
          <input
            value={agentFilter}
            onChange={(e) => setAgentFilter(e.target.value)}
            placeholder="any"
            className="rounded border border-[color:var(--border-color,#27272a)] bg-[color:var(--surface,#0b0b0e)] px-2 py-1 text-xs font-mono"
            data-testid="mcp-filter-agent"
          />
        </FieldLabel>
        <FieldLabel label="Tool">
          <input
            value={toolFilter}
            onChange={(e) => setToolFilter(e.target.value)}
            placeholder="any"
            className="rounded border border-[color:var(--border-color,#27272a)] bg-[color:var(--surface,#0b0b0e)] px-2 py-1 text-xs font-mono"
            data-testid="mcp-filter-tool"
          />
        </FieldLabel>
        <FieldLabel label="Outbound server">
          <input
            value={serverFilter}
            onChange={(e) => setServerFilter(e.target.value)}
            placeholder="any"
            className="rounded border border-[color:var(--border-color,#27272a)] bg-[color:var(--surface,#0b0b0e)] px-2 py-1 text-xs font-mono"
            data-testid="mcp-filter-server"
          />
        </FieldLabel>
        <FieldLabel label="Signature">
          <select
            value={sigFilter}
            onChange={(e) => setSigFilter(e.target.value as SignatureStatus | "all")}
            className="rounded border border-[color:var(--border-color,#27272a)] bg-[color:var(--surface,#0b0b0e)] px-2 py-1 text-xs font-mono"
            data-testid="mcp-filter-sig"
          >
            {SIG_FILTERS.map((s) => (
              <option key={s} value={s}>{s}</option>
            ))}
          </select>
        </FieldLabel>
      </section>

      <section aria-label="Tool usage heatmap" data-testid="mcp-heatmap-section">
        <h2 className="mb-2 text-sm font-semibold uppercase tracking-wider text-[color:var(--text-muted,#a1a1aa)]">
          Tool usage
        </h2>
        {usage.isError && (
          <div role="alert" className="rounded border border-red-500/40 bg-red-500/10 p-3 text-sm text-red-700 dark:text-red-300">
            Failed to load usage. <button type="button" className="underline" onClick={() => usage.refetch()}>Retry</button>
          </div>
        )}
        {usage.isLoading && !usage.data && (
          <div role="status" aria-busy="true" className="rounded border border-[color:var(--border-color,#27272a)] p-4 text-sm text-[color:var(--text-muted,#a1a1aa)]">
            Loading usage…
          </div>
        )}
        {usage.data && <ToolUsageHeatmap cells={usage.data.cells} />}
        {usage.data?.truncated_at_max && (
          <p className="mt-2 text-xs text-amber-600 dark:text-amber-400">
            Aggregation truncated at 100k events — narrow the time range for a complete count.
          </p>
        )}
      </section>

      <section aria-label="Approvals" data-testid="mcp-approvals-section">
        <h2 className="mb-2 text-sm font-semibold uppercase tracking-wider text-[color:var(--text-muted,#a1a1aa)]">
          Approval queue
        </h2>
        <div className="mb-3 inline-flex rounded border border-[color:var(--border-color,#27272a)]" role="tablist" aria-label="Approval status filter">
          {APPROVAL_STATUSES.map((s) => (
            <button
              key={s}
              type="button"
              role="tab"
              aria-selected={approvalStatus === s}
              onClick={() => setApprovalStatus(s)}
              className={`px-3 py-1 text-xs capitalize ${
                approvalStatus === s
                  ? "bg-[color:var(--accent,#a78bfa)]/20 text-[color:var(--accent,#a78bfa)]"
                  : "text-[color:var(--text-muted,#a1a1aa)]"
              }`}
              data-testid={`mcp-approval-tab-${s}`}
            >
              {s}
            </button>
          ))}
        </div>
        <MCPApprovalQueue status={approvalStatus} />
      </section>

      <section aria-label="Outbound calls" data-testid="mcp-outbound-section">
        <h2 className="mb-2 text-sm font-semibold uppercase tracking-wider text-[color:var(--text-muted,#a1a1aa)]">
          Outbound call log
        </h2>
        <MCPOutboundLog
          filters={{
            sinceMs: window.sinceMs,
            untilMs: window.untilMs,
            agent: agentFilter || undefined,
            server: serverFilter || undefined,
            sigStatus: sigFilter,
          }}
        />
      </section>

      <section aria-label="Prompt catalogue" data-testid="mcp-prompts-section">
        <PromptCatalogMount />
      </section>
    </div>
  );
}

// PromptCatalogMount isolates the prompts hook + component so the
// McpPage render function stays lean. When the gateway grows a live
// introspection endpoint (/api/v1/mcp/prompts), useMcpPrompts flips
// from the static catalogue to a React Query fetch and this wrapper
// stays unchanged.
function PromptCatalogMount() {
  const { data, isLoading, error } = useMcpPrompts();
  return <PromptCatalog prompts={data} isLoading={isLoading} error={error} />;
}

interface StatProps {
  label: string;
  value: number;
  hint?: string;
  tone?: "ok" | "warn";
}

function Stat({ label, value, hint, tone }: StatProps) {
  return (
    <div className="rounded border border-[color:var(--border-color,#27272a)] bg-[color:var(--surface-elevated,#111114)] p-3">
      <div className="text-[10px] uppercase tracking-wider text-[color:var(--text-muted,#a1a1aa)]">{label}</div>
      <div
        className={`mt-1 text-2xl font-mono tabular-nums ${
          tone === "warn"
            ? "text-amber-600 dark:text-amber-400"
            : "text-[color:var(--text,#e4e4e7)]"
        }`}
      >
        {value.toLocaleString()}
      </div>
      {hint && <div className="mt-1 text-xs text-[color:var(--text-muted,#a1a1aa)]">{hint}</div>}
    </div>
  );
}

function FieldLabel({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-1 text-[10px] uppercase tracking-wider text-[color:var(--text-muted,#a1a1aa)]">
      {label}
      {children}
    </label>
  );
}

export default MCPPage;
