// MCPOutboundLog — paginated feed of outbound MCP calls with
// signature-verification badges. Reads via the useMcpOutbound
// infinite query so the UI keeps memory bounded and the operator
// pulls additional pages on demand.
//
// Each row: tz-localised timestamp (UTC tooltip), agent (linked to
// agent detail page when known), tool_name, target_server, signature
// status badge (verified/unverified/invalid + key id), latency, and
// result_type. Signature status is the load-bearing visual: a tenant
// with `invalid` rows in this feed has a signing-key drift problem
// or an attacker on the wire.
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { useMcpOutbound, type OutboundQueryParams } from "../../hooks/useMcp";
import type { MCPOutboundEntry, SignatureStatus } from "../../api/types";
import { cn } from "../../lib/utils";

export interface MCPOutboundLogProps {
  filters: OutboundQueryParams;
  className?: string;
}

const SignatureClass: Record<SignatureStatus, string> = {
  verified:
    "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 ring-1 ring-emerald-500/30",
  unverified:
    "bg-amber-500/15 text-amber-700 dark:text-amber-300 ring-1 ring-amber-500/30",
  invalid: "bg-red-500/15 text-red-700 dark:text-red-300 ring-1 ring-red-500/30",
};

const SignatureLabel: Record<SignatureStatus, string> = {
  verified: "Signature verified",
  unverified: "Signature missing or unverified",
  invalid: "Signature invalid",
};

const SignatureGlyph: Record<SignatureStatus, string> = {
  verified: "✓",
  unverified: "•",
  invalid: "✕",
};

export function MCPOutboundLog({ filters, className }: MCPOutboundLogProps) {
  const query = useMcpOutbound(filters);
  const pages = query.data?.pages ?? [];
  const flat = pages.flatMap((p) => p.entries);
  const truncated = pages.some((p) => p.truncated_at_max);

  if (query.isLoading && flat.length === 0) {
    return (
      <div
        role="status"
        aria-busy="true"
        data-testid="mcp-outbound-loading"
        className={cn(
          "rounded-md border border-[color:var(--border-color,#27272a)] bg-[color:var(--surface,#0b0b0e)] p-4 text-sm text-[color:var(--text-muted,#a1a1aa)]",
          className,
        )}
      >
        Loading outbound MCP calls…
      </div>
    );
  }

  if (query.isError) {
    return (
      <div
        role="alert"
        data-testid="mcp-outbound-error"
        className={cn(
          "flex items-center justify-between rounded-md border border-red-500/40 bg-red-500/10 px-4 py-3 text-sm text-red-700 dark:text-red-300",
          className,
        )}
      >
        <span>Failed to load outbound MCP calls.</span>
        <button
          type="button"
          className="rounded bg-red-600 px-3 py-1 text-xs text-white hover:bg-red-500"
          onClick={() => query.refetch()}
        >
          Retry
        </button>
      </div>
    );
  }

  if (flat.length === 0) {
    return (
      <div
        data-testid="mcp-outbound-empty"
        className={cn(
          "rounded-md border border-dashed border-[color:var(--border-color,#27272a)] bg-[color:var(--surface,#0b0b0e)] p-6 text-center text-sm text-[color:var(--text-muted,#a1a1aa)]",
          className,
        )}
      >
        <p className="font-medium text-[color:var(--text,#e4e4e7)]">No outbound MCP calls yet.</p>
        <p className="mt-2">
          Enable signed outbound calls by setting <code className="font-mono">CORDUM_MCP_OUTBOUND_SIGNING_KEY</code>{" "}
          on the MCP bridge — once an agent makes its first signed
          outbound call it will appear here.
        </p>
      </div>
    );
  }

  return (
    <div
      data-testid="mcp-outbound-log"
      className={cn("flex flex-col gap-3", className)}
      aria-label="Outbound MCP call log"
    >
      <div className="overflow-x-auto rounded-md border border-[color:var(--border-color,#27272a)]">
        <table className="min-w-full divide-y divide-[color:var(--border-color,#27272a)] text-sm">
          <thead className="bg-[color:var(--surface-elevated,#111114)] text-[10px] uppercase tracking-wider text-[color:var(--text-muted,#a1a1aa)]">
            <tr>
              <Th>Time</Th>
              <Th>Agent</Th>
              <Th>Tool</Th>
              <Th>Target</Th>
              <Th>Signature</Th>
              <Th>Latency</Th>
              <Th>Result</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[color:var(--border-color,#27272a)] bg-[color:var(--surface,#0b0b0e)]">
            {flat.map((entry) => (
              <OutboundRow key={entry.stream_id} entry={entry} />
            ))}
          </tbody>
        </table>
      </div>

      <footer className="flex items-center justify-between text-xs text-[color:var(--text-muted,#a1a1aa)]">
        <span>
          {flat.length} call{flat.length === 1 ? "" : "s"}
          {truncated ? " (results may be partial — narrow the time range to see more)" : ""}
        </span>
        {query.hasNextPage && (
          <button
            type="button"
            data-testid="mcp-outbound-load-more"
            className="rounded border border-[color:var(--border-color,#27272a)] px-3 py-1 text-xs hover:bg-white/5 disabled:opacity-50"
            onClick={() => query.fetchNextPage()}
            disabled={query.isFetchingNextPage}
          >
            {query.isFetchingNextPage ? "Loading…" : "Load more"}
          </button>
        )}
      </footer>
    </div>
  );
}

function Th({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <th
      scope="col"
      className={cn("px-3 py-2 text-left font-mono", className)}
    >
      {children}
    </th>
  );
}

function OutboundRow({ entry }: { entry: MCPOutboundEntry }) {
  const ts = new Date(entry.ts_ms);
  return (
    <tr data-testid={`mcp-outbound-row-${entry.stream_id}`}>
      <td className="px-3 py-2 text-xs text-[color:var(--text,#e4e4e7)]" title={ts.toISOString()}>
        {ts.toLocaleString()}
      </td>
      <td className="px-3 py-2 font-mono text-xs">
        {entry.agent_id ? (
          <Link
            to={`/agents/${encodeURIComponent(entry.agent_id)}`}
            className="text-[color:var(--accent,#a78bfa)] hover:underline"
          >
            {entry.agent_id}
          </Link>
        ) : (
          <span className="text-[color:var(--text-muted,#a1a1aa)]">—</span>
        )}
      </td>
      <td className="px-3 py-2 font-mono text-xs">{entry.tool_name || "—"}</td>
      <td className="px-3 py-2 font-mono text-xs">{entry.target_server}</td>
      <td className="px-3 py-2">
        <SignatureBadge entry={entry} />
      </td>
      <td className="px-3 py-2 text-xs tabular-nums">
        {entry.latency_ms != null ? `${entry.latency_ms} ms` : "—"}
      </td>
      <td className="px-3 py-2 text-xs">
        {entry.result_type ? (
          <ResultBadge result={entry.result_type} />
        ) : (
          <span className="text-[color:var(--text-muted,#a1a1aa)]">—</span>
        )}
      </td>
    </tr>
  );
}

function SignatureBadge({ entry }: { entry: MCPOutboundEntry }) {
  const status = entry.signature_status;
  const aria = entry.signature_key_id
    ? `${SignatureLabel[status]} (key ${entry.signature_key_id})`
    : SignatureLabel[status];
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-sm px-2 py-0.5 text-xs font-medium",
        SignatureClass[status],
      )}
      aria-label={aria}
      data-testid={`mcp-outbound-sig-${entry.stream_id}`}
      data-status={status}
      title={entry.signature_key_id ? `key_id: ${entry.signature_key_id}` : undefined}
    >
      <span aria-hidden="true">{SignatureGlyph[status]}</span>
      <span>{status}</span>
    </span>
  );
}

function ResultBadge({ result }: { result: string }) {
  const isOk = result.toLowerCase() === "ok" || result.toLowerCase() === "success";
  const isErr = result.toLowerCase().includes("err") || result.toLowerCase() === "fail";
  return (
    <span
      className={cn(
        "inline-flex rounded-sm px-2 py-0.5 text-xs font-mono",
        isOk
          ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
          : isErr
            ? "bg-red-500/10 text-red-700 dark:text-red-300"
            : "bg-[color:var(--surface-subtle,rgba(120,120,120,0.1))] text-[color:var(--text,#e4e4e7)]",
      )}
    >
      {result}
    </span>
  );
}

export default MCPOutboundLog;
