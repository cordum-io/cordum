import { lazy, Suspense, useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/Button";
import { Drawer } from "@/components/ui/Drawer";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { decisionTone, type DecisionTone } from "@/lib/policy-studio/decision-tone";
import { useRuleAtVersion } from "@/hooks/useRuleAtVersion";
import { evaluatePolicy } from "@/api/generated/policy/policy";
import { DecisionType } from "@/api/generated/model/decisionType";
import { DecisionSource } from "@/api/generated/model/decisionSource";
import { logger } from "@/lib/logger";
import type { Decision } from "@/api/generated/model/decision";
import type { Rule } from "@/api/generated/model/rule";
import type { NormalizedRule } from "@/hooks/useRulesList";

const RuleMonacoEditor = lazy(() => import("./RuleMonacoEditor"));

const TONE_TO_BADGE: Record<DecisionTone, BadgeVariant> = {
  success: "healthy",
  warning: "warning",
  danger: "danger",
  info: "info",
  neutral: "muted",
};

interface WhatIfDrawerProps {
  open: boolean;
  onClose: () => void;
  decision: Decision;
}

interface HypotheticalState {
  loading: boolean;
  error: string | null;
  decisionType: DecisionType | null;
}

const INITIAL_HYPOTHETICAL: HypotheticalState = {
  loading: false,
  error: null,
  decisionType: null,
};

/**
 * Hypothetical re-evaluation drawer (D9b § DoD #2).
 *
 * Loads the firing rule via useRuleAtVersion(decision.rule_id), renders
 * its YAML in the shared RuleMonacoEditor, lets the user edit + click
 * Re-evaluate to see what the *active policy* would decide today.
 *
 * Spec § L141 makes "no save" load-bearing: closing the drawer or
 * navigating away discards the edited draft. We never call the rule
 * update mutation from this surface; users wanting to persist must open
 * the rule editor (via the row's "Open rule" cross-link).
 */
export function WhatIfDrawer({ open, onClose, decision }: WhatIfDrawerProps) {
  const ruleQuery = useRuleAtVersion(open ? decision.rule_id : null, decision.bundle_version);
  const [draft, setDraft] = useState<NormalizedRule | null>(null);
  const [hypothetical, setHypothetical] = useState<HypotheticalState>(INITIAL_HYPOTHETICAL);

  // Reset transient state every time the drawer opens fresh; closing +
  // reopening must not leak the prior session's draft or hypothetical.
  useEffect(() => {
    if (!open) {
      setDraft(null);
      setHypothetical(INITIAL_HYPOTHETICAL);
    }
  }, [open]);

  // Seed the draft from the fetched rule the first time it lands.
  useEffect(() => {
    if (open && ruleQuery.rule && !draft) {
      setDraft(ruleQuery.rule as NormalizedRule);
    }
  }, [open, ruleQuery.rule, draft]);

  const handleReevaluate = useCallback(async () => {
    const candidate = draft ?? (ruleQuery.rule as NormalizedRule | null);
    if (!candidate) return;
    setHypothetical({ loading: true, error: null, decisionType: null });
    try {
      const ruleForApi = candidate as unknown as Rule;
      const isEdge = decision.source === DecisionSource.edge;
      // Decision shape lacks tenant/topic/principal — synthesize the
      // minimum-required envelope so the evaluator at least runs. The
      // drawer copy makes the approximation explicit.
      const request = isEdge
        ? {
            rule: ruleForApi,
            edge_context: {
              tenant_id: "default",
              principal_id: "(unknown)",
            },
          }
        : {
            rule: ruleForApi,
            job_context: {
              tenant_id: "default",
              topic: decision.rule_id,
            },
          };
      const response = (await evaluatePolicy(request)) as {
        decision?: { type?: string } | string;
      };
      const next = extractDecisionType(response);
      if (!next) {
        setHypothetical({
          loading: false,
          error: "Evaluator returned an unexpected response shape",
          decisionType: null,
        });
        return;
      }
      setHypothetical({ loading: false, error: null, decisionType: next });
    } catch (err) {
      logger.warn("decisions-whatif", "re-evaluate failed", { err: String(err) });
      setHypothetical({
        loading: false,
        error: err instanceof Error ? err.message : "Re-evaluate request failed",
        decisionType: null,
      });
    }
  }, [decision.rule_id, decision.source, draft, ruleQuery.rule]);

  return (
    <Drawer
      open={open}
      onClose={onClose}
      size="xl"
      label={`What-if: ${decision.rule_id}`}
    >
      <div className="flex h-full flex-col gap-4">
        <header className="flex items-start justify-between">
          <div>
            <h2 className="font-display text-lg font-semibold text-ink">
              What-if for {decision.rule_id}
            </h2>
            <p className="text-xs text-muted-foreground">
              Edit the rule + re-evaluate against the active policy. Edits
              are not saved — close to discard.
            </p>
          </div>
          <Button
            variant="ghost"
            size="sm"
            onClick={onClose}
            aria-label="Close What-if drawer"
          >
            Close
          </Button>
        </header>

        <div className="grid grid-cols-2 gap-3">
          <DecisionPanel
            label="Actual"
            data-testid="whatif-actual"
            decisionType={decision.type}
            description={`Recorded ${decision.timestamp}`}
          />
          <HypotheticalPanel
            state={hypothetical}
            onReevaluate={handleReevaluate}
            ready={Boolean(draft || ruleQuery.rule)}
          />
        </div>

        <div className="flex-1 min-h-0">
          <RuleEditorRegion
            ruleQuery={ruleQuery}
            draft={draft}
            onChange={setDraft}
          />
        </div>
      </div>
    </Drawer>
  );
}

function DecisionPanel({
  label,
  decisionType,
  description,
  ...rest
}: {
  label: string;
  decisionType: DecisionType;
  description: string;
  "data-testid"?: string;
}) {
  const badge = TONE_TO_BADGE[decisionTone(decisionType)];
  return (
    <div
      data-testid={rest["data-testid"]}
      className="space-y-2 rounded-md border border-border/60 bg-surface-1 p-3"
    >
      <div className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
      <StatusBadge variant={badge}>{decisionType}</StatusBadge>
      <p className="text-xs text-muted-foreground">{description}</p>
    </div>
  );
}

function HypotheticalPanel({
  state,
  onReevaluate,
  ready,
}: {
  state: HypotheticalState;
  onReevaluate: () => void;
  ready: boolean;
}) {
  return (
    <div
      data-testid="whatif-hypothetical"
      className="space-y-2 rounded-md border border-border/60 bg-surface-1 p-3"
    >
      <div className="flex items-center justify-between">
        <div className="text-xs uppercase tracking-wide text-muted-foreground">
          Hypothetical
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={onReevaluate}
          loading={state.loading}
          disabled={state.loading || !ready}
          aria-label="Re-evaluate the edited rule"
        >
          Re-evaluate
        </Button>
      </div>
      {state.decisionType ? (
        <StatusBadge variant={TONE_TO_BADGE[decisionTone(state.decisionType)]}>
          {state.decisionType}
        </StatusBadge>
      ) : state.loading ? (
        <p className="text-xs italic text-muted-foreground">Evaluating...</p>
      ) : state.error ? (
        <p className="text-xs italic text-warning">{state.error}</p>
      ) : (
        <p className="text-xs italic text-muted-foreground">
          Click Re-evaluate to test the edited rule.
        </p>
      )}
    </div>
  );
}

function RuleEditorRegion({
  ruleQuery,
  draft,
  onChange,
}: {
  ruleQuery: ReturnType<typeof useRuleAtVersion>;
  draft: NormalizedRule | null;
  onChange: (next: NormalizedRule) => void;
}) {
  if (ruleQuery.loading) {
    return (
      <div data-testid="whatif-rule-loading" className="text-sm text-muted-foreground">
        Loading rule snapshot...
      </div>
    );
  }
  if (ruleQuery.error) {
    return (
      <div
        data-testid="whatif-rule-error"
        role="alert"
        className="rounded-md border border-warning/30 bg-warning/5 p-3 text-xs text-warning"
      >
        Couldn't load the rule: {ruleQuery.error.message}
      </div>
    );
  }
  if (!ruleQuery.rule) {
    return (
      <div
        data-testid="whatif-rule-error"
        role="alert"
        className="rounded-md border border-warning/30 bg-warning/5 p-3 text-xs text-warning"
      >
        Couldn't find the firing rule in the active policy.
      </div>
    );
  }
  const ruleToShow = draft ?? (ruleQuery.rule as NormalizedRule);
  return (
    <Suspense
      fallback={
        <div className="text-sm text-muted-foreground">Loading editor...</div>
      }
    >
      <RuleMonacoEditor rule={ruleToShow} onChange={onChange} />
    </Suspense>
  );
}

function extractDecisionType(response: unknown): DecisionType | null {
  if (!response || typeof response !== "object") return null;
  const obj = response as Record<string, unknown>;
  const dec = obj.decision;
  if (dec && typeof dec === "object" && !Array.isArray(dec)) {
    const t = (dec as Record<string, unknown>).type;
    if (typeof t === "string" && isKnownDecisionType(t)) {
      return t as DecisionType;
    }
  }
  if (typeof dec === "string" && isKnownDecisionType(dec)) {
    return dec as DecisionType;
  }
  return null;
}

function isKnownDecisionType(value: string): boolean {
  return Object.values(DecisionType).includes(value as DecisionType);
}
