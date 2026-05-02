/*
 * EDGE-024: EdgeEventInspector
 * Drawer-style inspector for a single AgentActionEvent.
 *
 * Renders identifiers, decision metadata, redacted summary, hashes, and
 * artifact-pointer metadata only. Raw bodies and prompt content stay on the
 * server: this component imports the AgentActionEvent type which has no raw
 * body fields, so a clean implementation cannot accidentally expose them.
 */
import { useMemo, useState } from "react";
import { Copy, Check } from "lucide-react";
import type { AgentActionEvent, EdgeArtifactPointer } from "@/api/types";
import { Drawer } from "@/components/ui/Drawer";
import { Button } from "@/components/ui/Button";
import { CodeBlock } from "@/components/ui/CodeBlock";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { cn } from "@/lib/utils";

interface EdgeEventInspectorProps {
  event: AgentActionEvent | null;
  open: boolean;
  onClose: () => void;
}

const decisionTone: Record<string, BadgeVariant> = {
  ALLOW: "healthy",
  DENY: "danger",
  REQUIRE_APPROVAL: "warning",
  REDACT: "warning",
  RECORDED: "info",
};

function decisionVariant(decision: AgentActionEvent["decision"]): BadgeVariant {
  return decisionTone[String(decision).toUpperCase()] ?? "info";
}

function formatBytes(value?: number): string {
  if (value === undefined || value === null || Number.isNaN(value)) return "—";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
  return `${(value / (1024 * 1024)).toFixed(2)} MiB`;
}

function formatDuration(ms?: number): string {
  if (ms === undefined || ms === null || Number.isNaN(ms)) return "—";
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

export function EdgeEventInspector({ event, open, onClose }: EdgeEventInspectorProps) {
  const redactedJson = useMemo(() => {
    if (!event?.inputRedacted) return "";
    try {
      return JSON.stringify(event.inputRedacted, null, 2);
    } catch {
      return "";
    }
  }, [event]);

  if (!event) {
    return (
      <Drawer open={open} onClose={onClose} size="xl" label="Edge event inspector">
        <div className="p-4 text-sm text-muted-foreground">No event selected.</div>
      </Drawer>
    );
  }

  return (
    <Drawer open={open} onClose={onClose} size="xl" label="Edge event inspector">
      <div className="space-y-5" data-testid="edge-event-inspector">
        <header className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 space-y-2">
            <p className="text-xs font-medium uppercase tracking-[0.2em] text-cordum">Edge event</p>
            <h2 className="break-all font-mono text-base font-semibold text-foreground" data-testid="edge-event-id">
              {event.eventId}
            </h2>
            <p className="text-xs text-muted-foreground">
              seq <span className="font-mono text-foreground">#{event.seq}</span> · {event.ts}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <span data-testid="edge-event-decision">
              <StatusBadge variant={decisionVariant(event.decision)}>
                {String(event.decision)}
              </StatusBadge>
            </span>
            <CopyButton value={event.eventId} label="Copy event id" />
          </div>
        </header>

        <FactGrid>
          <Fact label="Kind" value={event.kind} mono />
          <Fact label="Layer" value={event.layer} />
          <Fact label="Status" value={event.status} />
          <Fact label="Tool" value={event.toolName ?? "—"} />
          <Fact label="Capability" value={event.capability ?? "—"} />
          <Fact label="Action" value={event.actionName ?? "—"} />
          <Fact label="Duration" value={formatDuration(event.durationMs)} />
          <Fact label="Execution" value={event.executionId} mono />
        </FactGrid>

        {event.riskTags && event.riskTags.length > 0 ? (
          <section>
            <SectionTitle>Risk tags</SectionTitle>
            <div className="mt-2 flex flex-wrap gap-1.5">
              {event.riskTags.map((tag) => (
                <span
                  key={tag}
                  className="rounded-full border border-border px-2 py-0.5 text-[10px] uppercase tracking-[0.18em] text-muted-foreground"
                >
                  {tag}
                </span>
              ))}
            </div>
          </section>
        ) : null}

        <section>
          <SectionTitle>Decision</SectionTitle>
          <FactGrid>
            <Fact label="Reason" value={event.decisionReason ?? "—"} />
            <Fact label="Rule" value={event.ruleId ?? "—"} mono />
            <Fact label="Policy snapshot" value={event.policySnapshot ?? "—"} mono />
            <Fact
              label="Approval ref"
              value={event.approvalRef ?? "—"}
              mono
              testid="edge-event-approval-ref"
            />
          </FactGrid>
        </section>

        <section>
          <SectionTitle>Hashes</SectionTitle>
          <FactGrid>
            <Fact label="Input hash" value={event.inputHash ?? "—"} mono />
          </FactGrid>
        </section>

        {event.errorCode || event.errorMessage ? (
          <section>
            <SectionTitle>Error</SectionTitle>
            <FactGrid>
              <Fact label="Code" value={event.errorCode ?? "—"} mono />
              <Fact label="Message" value={event.errorMessage ?? "—"} />
            </FactGrid>
          </section>
        ) : null}

        <section>
          <SectionTitle>Redacted summary</SectionTitle>
          {redactedJson ? (
            <div className="mt-2">
              <CodeBlock language="json" maxHeight={280}>
                {redactedJson}
              </CodeBlock>
            </div>
          ) : (
            <p className="mt-2 text-sm text-muted-foreground">No redacted summary recorded.</p>
          )}
          <p className="mt-2 text-xs text-muted-foreground">
            Server-side redaction strips secrets, raw bodies, and prompt content before persistence; only
            sanitized identifiers and labels reach this view.
          </p>
        </section>

        <ArtifactPointers artifacts={event.artifactPtrs ?? []} />
      </div>
    </Drawer>
  );
}

function ArtifactPointers({ artifacts }: { artifacts: EdgeArtifactPointer[] }) {
  if (artifacts.length === 0) {
    return (
      <section>
        <SectionTitle>Artifact pointers</SectionTitle>
        <p className="mt-2 text-sm text-muted-foreground">No artifact pointers attached to this event.</p>
      </section>
    );
  }
  return (
    <section data-testid="edge-event-artifacts">
      <SectionTitle>Artifact pointers</SectionTitle>
      <ul className="mt-2 space-y-2">
        {artifacts.map((artifact) => (
          <li
            key={`${artifact.sha256}:${artifact.uri}`}
            className="rounded-2xl border border-border bg-surface-1/70 p-3"
          >
            <FactGrid>
              <Fact label="Kind" value={artifact.artifactType ?? "—"} />
              <Fact label="Size" value={formatBytes(artifact.sizeBytes)} />
              <Fact label="Hash" value={artifact.sha256 ?? "—"} mono />
              <Fact label="URI" value={artifact.uri} mono />
            </FactGrid>
          </li>
        ))}
      </ul>
    </section>
  );
}

function FactGrid({ children }: { children: React.ReactNode }) {
  return <div className="grid gap-3 sm:grid-cols-2">{children}</div>;
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <p className="text-[10px] font-medium uppercase tracking-[0.2em] text-cordum">{children}</p>
  );
}

function Fact({
  label,
  value,
  mono,
  testid,
}: {
  label: string;
  value: string | number;
  mono?: boolean;
  testid?: string;
}) {
  return (
    <div className="min-w-0">
      <div className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">{label}</div>
      <div
        data-testid={testid}
        className={cn("mt-1 break-all text-sm text-foreground", mono && "font-mono text-xs")}
      >
        {value}
      </div>
    </div>
  );
}

function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1500);
        } catch {
          /* ignore — clipboard unavailable */
        }
      }}
      aria-label={label}
    >
      {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
      {copied ? "Copied" : "Copy"}
    </Button>
  );
}
