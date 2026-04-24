import { useMemo } from "react";
import YAML from "yaml";
import { StatusBadge } from "@/components/ui/StatusBadge";

interface BundleVisualPreviewProps {
  yaml: string;
  onSwitchToCode?: () => void;
}

interface ParsedRule {
  id: string;
  decision: string;
  reason: string;
}

function extractRules(yaml: string): { rules: ParsedRule[]; error: string | null } {
  if (!yaml.trim()) return { rules: [], error: null };
  try {
    const doc = YAML.parse(yaml) as Record<string, unknown> | null;
    if (!doc || typeof doc !== "object") return { rules: [], error: null };

    const rawRules = Array.isArray(doc.rules)
      ? doc.rules
      : Array.isArray(doc.input_rules)
        ? doc.input_rules
        : [];

    const rules: ParsedRule[] = rawRules
      .filter((r): r is Record<string, unknown> => r && typeof r === "object")
      .map((r) => ({
        id: String(r.id ?? ""),
        decision: String(r.decision ?? r.decision_type ?? ""),
        reason: String(r.reason ?? ""),
      }));

    return { rules, error: null };
  } catch (err) {
    return { rules: [], error: err instanceof Error ? err.message : "YAML parse error" };
  }
}

function decisionVariant(decision: string): "healthy" | "warning" | "governance" | "muted" {
  const d = decision.toLowerCase();
  if (d === "allow") return "healthy";
  if (d === "deny") return "governance";
  if (d === "require_approval" || d === "throttle") return "warning";
  return "muted";
}

export function BundleVisualPreview({ yaml, onSwitchToCode }: BundleVisualPreviewProps) {
  const { rules, error } = useMemo(() => extractRules(yaml), [yaml]);

  if (error) {
    return (
      <div className="rounded-2xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-xs text-destructive space-y-2">
        <p>Unable to parse YAML for preview: {error}</p>
        {onSwitchToCode && (
          <button
            type="button"
            onClick={onSwitchToCode}
            className="text-xs font-medium text-accent hover:underline"
          >
            Switch to Code tab to fix syntax errors
          </button>
        )}
      </div>
    );
  }

  if (rules.length === 0) {
    return (
      <div className="rounded-xl border border-border bg-surface-1 p-4 text-xs text-muted-foreground">
        No rules found in bundle content.
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <p className="text-xs font-mono uppercase tracking-wider text-muted-foreground">
        {rules.length} rule{rules.length !== 1 ? "s" : ""}
      </p>
      <div className="divide-y divide-border rounded-xl border border-border">
        {rules.map((rule, index) => (
          <div key={rule.id || index} className="flex items-start gap-3 px-4 py-3">
            <span className="shrink-0 text-xs font-mono text-muted-foreground/60 pt-0.5">
              {index + 1}
            </span>
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2 mb-1">
                <p className="text-xs font-mono text-foreground truncate">{rule.id || "(no id)"}</p>
                {rule.decision && (
                  <StatusBadge variant={decisionVariant(rule.decision)}>
                    {rule.decision}
                  </StatusBadge>
                )}
              </div>
              {rule.reason && (
                <p className="text-xs text-muted-foreground">{rule.reason}</p>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

