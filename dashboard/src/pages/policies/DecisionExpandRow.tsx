import { useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { ArrowRight, Link2, Play, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { CodeBlock } from "@/components/ui/CodeBlock";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { useArtifact } from "@/hooks/useArtifact";
import { decisionTone, type DecisionTone } from "@/lib/policy-studio/decision-tone";
import { useReplayDecision } from "@/hooks/useReplayDecision";
import type { Decision } from "@/api/generated/model/decision";
import type { TraceStep } from "@/api/generated/model/traceStep";
import { WhatIfDrawer } from "./WhatIfDrawer";

const TONE_TO_BADGE: Record<DecisionTone, BadgeVariant> = {
  success: "healthy",
  warning: "warning",
  danger: "danger",
  info: "info",
  neutral: "muted",
};

interface DecisionExpandRowProps {
  decision: Decision;
}

/**
 * Decisions surface — expand-row content (D8b + D9b).
 *
 * D8b shipped the four sections (Trace / Input / Bundle context / Actions).
 * D9b wires the action handlers:
 *   - Replay → useReplayDecision (POST /api/v1/policy/replay) with inline
 *     was/now badge delta below the action row.
 *   - What-if → opens WhatIfDrawer with the firing rule loaded for
 *     hypothetical edit + re-evaluate. No save.
 *   - Open rule → unchanged D8b cross-link to the rule editor.
 */
export function DecisionExpandRow({ decision }: DecisionExpandRowProps) {
  const artifact = useArtifact(decision.input_ref);
  const trace = decision.trace ?? [];
  const replay = useReplayDecision();
  const [whatIfOpen, setWhatIfOpen] = useState(false);

  return (
    <div
      className="space-y-4 rounded-2xl border border-border/60 bg-surface-1 p-4"
      data-testid="decision-expand-row"
    >
      <Section
        title="Trace"
        subtitle={`${trace.length} rule evaluation${trace.length === 1 ? "" : "s"}`}
      >
        {trace.length === 0 ? (
          <p className="text-xs italic text-muted-foreground">
            Awaiting Backend 3/4 backfill - trace persistence is incremental.
          </p>
        ) : (
          <ol className="ml-1 space-y-2 border-l border-border/60 pl-4">
            {trace.map((step, index) => (
              <TraceRow key={`${step.rule_id}-${index}`} step={step} />
            ))}
          </ol>
        )}
      </Section>

      <Section
        title="Input"
        subtitle={
          decision.input_ref
            ? `ref ${decision.input_ref}`
            : "No input ref recorded for this decision"
        }
      >
        {!decision.input_ref ? (
          <p className="text-xs italic text-muted-foreground">
            No input ref recorded - pre-Backend-1 rows ship without artifact pointers.
          </p>
        ) : artifact.error ? (
          <p className="text-xs italic text-warning">
            Couldn't resolve artifact: {artifact.error.message}
          </p>
        ) : artifact.loading ? (
          <p className="text-xs italic text-muted-foreground">Loading artifact...</p>
        ) : (
          <CodeBlock
            language="json"
            title={decision.input_ref}
            collapsible
            defaultExpanded={false}
          >
            {artifact.data || "(empty artifact body)"}
          </CodeBlock>
        )}
      </Section>

      <Section title="Bundle context" subtitle="Bundle binding + audit hash">
        <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
          <dt className="text-muted-foreground">Bundle</dt>
          <dd className="font-mono text-foreground">
            {decision.bundle_id ?? "-"}
          </dd>
          <dt className="text-muted-foreground">Version</dt>
          <dd className="font-mono text-foreground">
            {decision.bundle_version ?? "-"}
          </dd>
          <dt className="text-muted-foreground">Audit hash</dt>
          <dd>
            <CodeBlock
              inline
              copyable
              ariaLabel={`Copy ${decision.audit_hash ?? ""}`}
              inlineMaxLength={8}
              inlineTitle={
                decision.audit_hash
                  ? `${decision.audit_hash} - click to copy`
                  : "No audit hash"
              }
            >
              {decision.audit_hash ?? ""}
            </CodeBlock>
          </dd>
          <dt className="text-muted-foreground">Source</dt>
          <dd className="font-mono text-foreground">{decision.source}</dd>
        </dl>
        {decision.audit_hash && (
          <div className="mt-3">
            <Link
              to={`/audit?search=${encodeURIComponent(decision.audit_hash)}`}
              data-row-action="cross-link-decisions-audit"
              aria-label="View this decision in the full audit chain"
              className="inline-flex items-center gap-1 text-xs font-medium text-cordum hover:underline"
            >
              <Link2 className="h-3 w-3" aria-hidden />
              View in full audit chain
            </Link>
          </div>
        )}
      </Section>

      <Section title="Actions" subtitle="Replay, What-if, and open in rule editor">
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => replay.mutate(decision)}
            loading={replay.isPending}
            disabled={replay.isPending}
            aria-label="Replay this decision against the active policy"
          >
            <Play className="mr-1 h-3.5 w-3.5" aria-hidden />
            Replay
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setWhatIfOpen(true)}
            aria-label="Open What-if drawer for this rule"
          >
            <Sparkles className="mr-1 h-3.5 w-3.5" aria-hidden />
            What-if
          </Button>
          <Link
            to={`/policies?rule=${encodeURIComponent(decision.rule_id)}&open=editor`}
            aria-label={`Open rule ${decision.rule_id} in editor`}
            className="inline-flex h-8 items-center gap-1 rounded-md border border-border bg-surface-1 px-3 text-xs font-medium text-foreground transition-colors hover:bg-surface-2 hover:text-cordum"
          >
            Open rule
            <ArrowRight className="h-3.5 w-3.5" aria-hidden />
          </Link>
        </div>
        <ReplayResult
          isPending={replay.isPending}
          isError={replay.isError}
          error={replay.error}
          data={replay.data ?? null}
        />
      </Section>

      <WhatIfDrawer
        open={whatIfOpen}
        onClose={() => setWhatIfOpen(false)}
        decision={decision}
      />
    </div>
  );
}

interface ReplayResultProps {
  isPending: boolean;
  isError: boolean;
  error: Error | null;
  data: { was: import("@/api/generated/model/decisionType").DecisionType; now: import("@/api/generated/model/decisionType").DecisionType; bundleVersion: string; changed: boolean } | null;
}

function ReplayResult({ isPending, isError, error, data }: ReplayResultProps) {
  if (isPending) {
    return (
      <div
        data-testid="decision-replay-loading"
        className="mt-3 flex items-center gap-2 rounded-md border border-dashed border-border/60 bg-surface-1 px-3 py-2 text-xs italic text-muted-foreground"
      >
        Replaying against the active policy...
      </div>
    );
  }
  if (isError) {
    return (
      <div
        data-testid="decision-replay-error"
        role="alert"
        className="mt-3 rounded-md border border-warning/30 bg-warning/5 px-3 py-2 text-xs text-warning"
      >
        Couldn't replay this decision: {error?.message ?? "unknown error"}
      </div>
    );
  }
  if (!data) return null;

  const wasBadge = TONE_TO_BADGE[decisionTone(data.was)];
  const nowBadge = TONE_TO_BADGE[decisionTone(data.now)];
  const containerClass = data.changed
    ? "border-warning/40 bg-warning/5"
    : "border-border/60 bg-surface-1";
  return (
    <div
      data-testid="decision-replay-result"
      data-changed={data.changed ? "true" : "false"}
      className={`mt-3 flex flex-wrap items-center gap-2 rounded-md border px-3 py-2 text-xs ${containerClass}`}
    >
      {data.changed ? (
        <>
          <span className="font-medium text-foreground">If evaluated now:</span>
          <StatusBadge variant={nowBadge}>{data.now}</StatusBadge>
          <span className="text-muted-foreground">(was:</span>
          <StatusBadge variant={wasBadge}>{data.was}</StatusBadge>
          <span className="text-muted-foreground">)</span>
        </>
      ) : (
        <>
          <span className="font-medium text-foreground">No change:</span>
          <span className="text-muted-foreground">
            still <StatusBadge variant={wasBadge}>{data.was}</StatusBadge> under the active policy
          </span>
        </>
      )}
      {data.bundleVersion && (
        <span className="ml-auto font-mono text-[10px] text-muted-foreground">
          {data.bundleVersion}
        </span>
      )}
    </div>
  );
}

function Section({
  title,
  subtitle,
  children,
}: {
  title: string;
  subtitle?: string;
  children: ReactNode;
}) {
  return (
    <section className="space-y-2">
      <header className="flex items-baseline gap-2">
        <h3 className="font-display text-sm font-semibold text-ink">{title}</h3>
        {subtitle && (
          <span className="text-xs text-muted-foreground">{subtitle}</span>
        )}
      </header>
      <div className="pl-1">{children}</div>
    </section>
  );
}

function TraceRow({ step }: { step: TraceStep }) {
  const tone = decisionTone(step.decision_type);
  return (
    <li className="relative">
      <span
        className="absolute -left-[1.25rem] top-1.5 h-2 w-2 rounded-full bg-muted-foreground/60"
        aria-hidden
      />
      <div className="flex flex-wrap items-center gap-2">
        <code className="font-mono text-xs text-foreground">{step.rule_id}</code>
        <StatusBadge variant={TONE_TO_BADGE[tone]}>
          {step.decision_type}
        </StatusBadge>
        {step.bundle_id && (
          <span className="font-mono text-[10px] text-muted-foreground">
            in {step.bundle_id}
          </span>
        )}
      </div>
      {step.reason && (
        <p className="mt-1 text-xs text-muted-foreground">{step.reason}</p>
      )}
    </li>
  );
}
