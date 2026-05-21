/*
 * EDGE-105 — MCPInspector
 * Inline (row-expand) inspector body for a single AgentActionEvent on the
 * MCP lane. Renders six fields plus an optional artifact-pointer chip.
 *
 * Defense-in-depth: every user-facing payload string is run through
 * `sanitizeMCPField` / `sanitizeMCPPayload` before display, so even if a
 * raw sensitive field slips past server-side redaction it never reaches
 * the DOM. Server-side redaction (the `_redacted` suffix contract) is
 * trusted verbatim and shown as-is.
 */

import { Link } from "react-router-dom";
import { Paperclip } from "lucide-react";
import type { AgentActionEvent } from "@/api/types";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { cn } from "@/lib/utils";
import { sanitizeMCPField, sanitizeMCPPayload } from "@/lib/redaction";

interface MCPInspectorProps {
  event: AgentActionEvent;
}

const DECISION_TONE: Record<string, BadgeVariant> = {
  ALLOW: "healthy",
  DENY: "danger",
  REQUIRE_APPROVAL: "warning",
  REDACT: "warning",
  RECORDED: "info",
  THROTTLE: "warning",
  CONSTRAIN: "info",
};

function decisionVariant(decision: AgentActionEvent["decision"]): BadgeVariant {
  return DECISION_TONE[String(decision).toUpperCase()] ?? "info";
}

function shortSha256(sha: string | undefined): string {
  if (!sha) return "";
  const stripped = sha.startsWith("sha256:") ? sha.slice(7) : sha;
  return stripped.length > 12 ? stripped.slice(0, 12) : stripped;
}

export function MCPInspector({ event }: MCPInspectorProps) {
  const labels = event.labels ?? {};
  const upstreamServer = labels.mcp_server ?? event.agentProduct ?? "—";
  const toolName = event.toolName ?? "—";
  const approvalRef = event.approvalRef ?? "";
  const argsDisplay = sanitizeMCPPayload(event.inputRedacted ?? undefined, event.inputRedacted ?? undefined);
  const resultDisplay = sanitizeMCPField(undefined, labels, "result");
  const artifact = event.artifactPtrs && event.artifactPtrs.length > 0 ? event.artifactPtrs[0] : null;

  return (
    <div
      data-testid={`mcp-inspector-${event.eventId}`}
      className="space-y-4 border-t border-cyan-300/30 bg-surface-1/50 px-3 py-3"
    >
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <Field label="Upstream server" testid="mcp-inspector-upstream-server" mono>
          {upstreamServer}
        </Field>
        <Field label="Tool" testid="mcp-inspector-tool-name" mono>
          {toolName}
        </Field>
        <Field label="Decision" testid="mcp-inspector-decision">
          <StatusBadge variant={decisionVariant(event.decision)}>{String(event.decision)}</StatusBadge>
        </Field>
        <Field label="Approval" testid="mcp-inspector-approval-ref" mono>
          {approvalRef ? (
            <Link
              to={`/approvals/${approvalRef}`}
              data-testid="mcp-inspector-approval-ref-link"
              className="font-mono text-xs text-cordum underline-offset-2 hover:underline"
            >
              {approvalRef}
            </Link>
          ) : (
            <span>—</span>
          )}
        </Field>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <PayloadField label="Args (redacted)" testid="mcp-inspector-args">
          {argsDisplay}
        </PayloadField>
        <PayloadField label="Result (redacted)" testid="mcp-inspector-result">
          {resultDisplay}
        </PayloadField>
      </div>
      {artifact ? (
        <div>
          <a
            href={artifact.uri}
            rel="noopener noreferrer"
            target="_blank"
            data-testid="mcp-inspector-artifact-chip"
            className="inline-flex items-center gap-1.5 rounded-full border border-border bg-surface-1/70 px-2.5 py-1 text-[10px] font-mono text-muted-foreground transition-colors hover:border-cyan-300/40 hover:text-cyan-600 dark:hover:text-cyan-300"
          >
            <Paperclip className="h-3 w-3" aria-hidden="true" />
            <span>{shortSha256(artifact.sha256)}</span>
            <span className="text-[10px] uppercase tracking-[0.12em]">artifact</span>
          </a>
        </div>
      ) : null}
    </div>
  );
}

function Field({
  label,
  testid,
  mono,
  children,
}: {
  label: string;
  testid: string;
  mono?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="min-w-0">
      <div className="text-[10px] font-medium uppercase tracking-[0.18em] text-muted-foreground">
        {label}
      </div>
      <div
        data-testid={testid}
        className={cn("mt-1 break-all text-sm text-foreground", mono && "font-mono text-xs")}
      >
        {children}
      </div>
    </div>
  );
}

function PayloadField({
  label,
  testid,
  children,
}: {
  label: string;
  testid: string;
  children: string;
}) {
  return (
    <div className="min-w-0">
      <div className="text-[10px] font-medium uppercase tracking-[0.18em] text-muted-foreground">
        {label}
      </div>
      <pre
        data-testid={testid}
        className="mt-1 max-h-48 overflow-auto rounded-xl border border-border bg-surface-1/70 px-2.5 py-1.5 text-[11px] font-mono leading-relaxed text-foreground"
      >
        {children}
      </pre>
    </div>
  );
}
