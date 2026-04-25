import { useState, useEffect, useCallback } from "react";
import { Play } from "lucide-react";
import { Button } from "@/components/ui/Button";

export interface SimulatorContext {
  topic: string;
  tenant: string;
  workflowId: string;
  capabilities: string[];
  riskTags: string[];
  labels: Record<string, string>;
}

interface SimulatorContextFormProps {
  prefill?: Partial<SimulatorContext>;
  isSubmitting: boolean;
  onSubmit: (ctx: SimulatorContext) => void;
}

function parseCsv(raw: string): string[] {
  return raw.split(",").map((s) => s.trim()).filter(Boolean);
}

function parseLabels(raw: string): Record<string, string> {
  const labels: Record<string, string> = {};
  for (const pair of raw.split(",")) {
    const idx = pair.indexOf("=");
    if (idx > 0) {
      const key = pair.slice(0, idx).trim();
      const value = pair.slice(idx + 1).trim();
      if (key) labels[key] = value;
    }
  }
  return labels;
}

function labelsToString(labels: Record<string, string>): string {
  return Object.entries(labels).map(([k, v]) => `${k}=${v}`).join(", ");
}

/** @internal exported for testing */
export const __simulatorContextFormInternal = {
  parseCsv,
  parseLabels,
  labelsToString,
};

export function SimulatorContextForm({
  prefill,
  isSubmitting,
  onSubmit,
}: SimulatorContextFormProps) {
  const [topic, setTopic] = useState(prefill?.topic ?? "");
  const [tenant, setTenant] = useState(prefill?.tenant ?? "");
  const [workflowId, setWorkflowId] = useState(prefill?.workflowId ?? "");
  const [capabilitiesRaw, setCapabilitiesRaw] = useState(
    prefill?.capabilities?.join(", ") ?? "",
  );
  const [riskTagsRaw, setRiskTagsRaw] = useState(
    prefill?.riskTags?.join(", ") ?? "",
  );
  const [labelsRaw, setLabelsRaw] = useState(
    prefill?.labels ? labelsToString(prefill.labels) : "",
  );

  // Apply prefill changes (e.g. from deep-link navigation). Always replace
  // (not just on truthy) so a prefill that explicitly clears a field — and
  // labelsRaw, which the previous implementation skipped entirely — both
  // sync to the form state.
  useEffect(() => {
    if (!prefill) return;
    setTopic(prefill.topic ?? "");
    setTenant(prefill.tenant ?? "");
    setWorkflowId(prefill.workflowId ?? "");
    setCapabilitiesRaw(prefill.capabilities?.join(", ") ?? "");
    setRiskTagsRaw(prefill.riskTags?.join(", ") ?? "");
    setLabelsRaw(prefill.labels ? labelsToString(prefill.labels) : "");
  }, [prefill]);

  const handleSubmit = useCallback(
    (e: React.FormEvent) => {
      e.preventDefault();
      onSubmit({
        topic,
        tenant,
        workflowId,
        capabilities: parseCsv(capabilitiesRaw),
        riskTags: parseCsv(riskTagsRaw),
        labels: parseLabels(labelsRaw),
      });
    },
    [topic, tenant, workflowId, capabilitiesRaw, riskTagsRaw, labelsRaw, onSubmit],
  );

  const canSubmit = topic.trim().length > 0;

  return (
    <form onSubmit={handleSubmit} className="instrument-card space-y-4">
      <div>
        <h2 className="text-sm font-display font-semibold text-foreground">
          Simulation context
        </h2>
        <p className="text-xs text-muted-foreground mt-1">
          Configure the request payload for policy evaluation. Topic is required.
        </p>
      </div>

      <div className="space-y-3">
        <label className="block text-xs text-muted-foreground">
          Topic <span className="text-destructive">*</span>
          <input
            className="mt-1 h-8 w-full rounded-xl border border-border bg-surface-2 px-3 text-xs text-foreground"
            placeholder="job.customer.deploy"
            value={topic}
            onChange={(e) => setTopic(e.target.value)}
          />
        </label>

        <label className="block text-xs text-muted-foreground">
          Tenant ID
          <input
            className="mt-1 h-8 w-full rounded-xl border border-border bg-surface-2 px-3 text-xs text-foreground"
            placeholder="acme-corp"
            value={tenant}
            onChange={(e) => setTenant(e.target.value)}
          />
        </label>

        <label className="block text-xs text-muted-foreground">
          Workflow ID
          <input
            className="mt-1 h-8 w-full rounded-xl border border-border bg-surface-2 px-3 text-xs text-foreground"
            placeholder="wf-deploy-prod (optional)"
            value={workflowId}
            onChange={(e) => setWorkflowId(e.target.value)}
          />
        </label>

        <label className="block text-xs text-muted-foreground">
          Capabilities
          <input
            className="mt-1 h-8 w-full rounded-xl border border-border bg-surface-2 px-3 text-xs text-foreground"
            placeholder="code.generate, code.review (comma-separated)"
            value={capabilitiesRaw}
            onChange={(e) => setCapabilitiesRaw(e.target.value)}
          />
        </label>

        <label className="block text-xs text-muted-foreground">
          Risk tags
          <input
            className="mt-1 h-8 w-full rounded-xl border border-border bg-surface-2 px-3 text-xs text-foreground"
            placeholder="high, pii, admin (comma-separated)"
            value={riskTagsRaw}
            onChange={(e) => setRiskTagsRaw(e.target.value)}
          />
        </label>

        <label className="block text-xs text-muted-foreground">
          Labels
          <input
            className="mt-1 h-8 w-full rounded-xl border border-border bg-surface-2 px-3 text-xs text-foreground"
            placeholder="env=prod, team=platform (key=value, comma-separated)"
            value={labelsRaw}
            onChange={(e) => setLabelsRaw(e.target.value)}
          />
        </label>
      </div>

      <Button
        type="submit"
        size="sm"
        disabled={!canSubmit || isSubmitting}
        className="w-full"
      >
        <Play className="mr-1.5 h-3.5 w-3.5" />
        {isSubmitting ? "Evaluating..." : "Run simulation"}
      </Button>
    </form>
  );
}

