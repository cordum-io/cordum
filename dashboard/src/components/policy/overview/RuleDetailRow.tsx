import { useState, useMemo } from "react";
import { cn } from "@/lib/utils";
import { logger } from "@/lib/logger";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";
import { StatusBadge } from "@/components/ui/StatusBadge";
import {
  ChevronDown,
  Code2,
  Eye,
  EyeOff,
  Shield,
  Network,
  Tag,
  Clock,
  Hash,
  Layers,
  AlertTriangle,
  Copy,
  Check,
} from "lucide-react";
import type { PolicyRule } from "@/api/types";
import YAML from "yaml";

/* ── helpers ── */

function ruleToYaml(rule: PolicyRule): string {
  const obj: Record<string, unknown> = {
    id: rule.id,
    name: rule.name,
    ...(rule.description && { description: rule.description }),
    decision: rule.decision,
    priority: rule.priority,
    enabled: rule.enabled,
    match: { ...rule.match },
  };
  if (rule.velocity) {
    obj.velocity = rule.velocity;
  }
  if (rule.constraints && Object.keys(rule.constraints).length > 0) {
    obj.constraints = rule.constraints;
  }
  if (rule.reason) {
    obj.reason = rule.reason;
  }
  return YAML.stringify(obj, { lineWidth: 80 });
}

type RuleType = "velocity" | "allowlist" | "threshold" | "scope" | "scanner" | "standard";

function detectRuleType(rule: PolicyRule): RuleType {
  if (rule.velocity) return "velocity";
  if (rule.match?.label_allowlist && Object.keys(rule.match.label_allowlist).length > 0) return "allowlist";
  if (rule.match?.label_threshold && Object.keys(rule.match.label_threshold).length > 0) return "threshold";
  // Check if this is a scope rule (has input rule with scope config) — heuristic: rule ID contains "scope"
  if (rule.id?.includes("scope")) return "scope";
  return "standard";
}

const RULE_TYPE_BADGE: Record<RuleType, { label: string; className: string }> = {
  velocity: { label: "Velocity", className: "bg-[var(--color-warning)]/10 text-[var(--color-warning)] border-[var(--color-warning)]/20" },
  allowlist: { label: "Allowlist", className: "bg-[var(--color-info)]/10 text-[var(--color-info)] border-[var(--color-info)]/20" },
  threshold: { label: "Threshold", className: "bg-destructive/10 text-destructive border-destructive/20" },
  scope: { label: "Scope", className: "bg-[var(--color-governance)]/10 text-[var(--color-governance)] border-[var(--color-governance)]/20" },
  scanner: { label: "Scanner", className: "bg-[var(--color-success)]/10 text-[var(--color-success)] border-[var(--color-success)]/20" },
  standard: { label: "Standard", className: "bg-muted/50 text-muted-foreground border-border" },
};

function formatMatchValue(val: unknown): string {
  if (Array.isArray(val)) return val.join(", ");
  if (typeof val === "object" && val !== null) return JSON.stringify(val);
  return String(val ?? "—");
}

/* ── component ── */

interface RuleDetailRowProps {
  rule: PolicyRule;
  index: number;
  className?: string;
}

export function RuleDetailRow({ rule, index, className }: RuleDetailRowProps) {
  const [expanded, setExpanded] = useState(false);
  const [showYaml, setShowYaml] = useState(false);
  const ruleType = useMemo(() => detectRuleType(rule), [rule]);
  const typeBadge = RULE_TYPE_BADGE[ruleType];
  const [copied, setCopied] = useState(false);

  const yamlStr = useMemo(() => ruleToYaml(rule), [rule]);

  const matchEntries = useMemo(() => {
    if (!rule.match) return [];
    return Object.entries(rule.match).filter(
      ([, v]) =>
        v !== undefined &&
        v !== null &&
        !(Array.isArray(v) && v.length === 0) &&
        !(typeof v === "object" && !Array.isArray(v) && Object.keys(v as object).length === 0),
    );
  }, [rule.match]);

  const hasConstraints =
    rule.constraints && Object.keys(rule.constraints).length > 0;

  const constraintEntries = useMemo(() => {
    if (!hasConstraints) return [];
    const flat: { section: string; key: string; value: string }[] = [];
    const c = rule.constraints!;
    if (c.budgets) {
      for (const [k, v] of Object.entries(c.budgets)) {
        if (v !== undefined) flat.push({ section: "Budgets", key: k, value: String(v) });
      }
    }
    if (c.sandbox) {
      for (const [k, v] of Object.entries(c.sandbox)) {
        if (v !== undefined)
          flat.push({ section: "Sandbox", key: k, value: Array.isArray(v) ? v.join(", ") : String(v) });
      }
    }
    if (c.toolchain) {
      for (const [k, v] of Object.entries(c.toolchain)) {
        if (v !== undefined)
          flat.push({ section: "Toolchain", key: k, value: Array.isArray(v) ? v.join(", ") : String(v) });
      }
    }
    if (c.diff) {
      for (const [k, v] of Object.entries(c.diff)) {
        if (v !== undefined)
          flat.push({ section: "Diff", key: k, value: Array.isArray(v) ? v.join(", ") : String(v) });
      }
    }
    if (c.redaction_level) {
      flat.push({ section: "Redaction", key: "level", value: c.redaction_level });
    }
    return flat;
  }, [rule.constraints, hasConstraints]);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(yamlStr);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (err) {
      // navigator.clipboard.writeText rejects in insecure contexts or when
      // permission is denied — surface a structured warn instead of an
      // unhandled rejection, and skip the success state so the UI stays
      // truthful.
      logger.warn("rule-detail-row", "clipboard copy failed", { error: String(err) });
    }
  };

  return (
    <div
      className={cn(
        "group border border-border rounded-2xl bg-[var(--surface-glass)] transition-all duration-200",
        expanded && "border-cordum/30",
        !expanded && "hover:border-cordum/20 hover:-translate-y-px hover:shadow-[0_6px_16px_rgba(0,0,0,0.2)]",
        !rule.enabled && "opacity-60",
        className,
      )}
    >
      {/* ── Collapsed header ── */}
      <button
        type="button"
        onClick={() => setExpanded(!expanded)}
        className="w-full text-left px-4 py-3 flex items-start gap-3"
      >
        {/* Order badge */}
        <span className="shrink-0 mt-0.5 font-mono text-xs text-muted-foreground bg-surface-2 rounded-xl px-2 py-1 min-w-[28px] text-center">
          #{index + 1}
        </span>

        {/* Main info */}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-sm font-semibold text-foreground truncate">
              {rule.name}
            </span>
            <SafetyDecisionBadge decision={rule.decision} />
            <span className={cn("inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium border", typeBadge.className)}>
              {typeBadge.label}
            </span>
            {!rule.enabled && (
              <StatusBadge variant="muted">Disabled</StatusBadge>
            )}
            {hasConstraints && (
              <StatusBadge variant="info">Constrained</StatusBadge>
            )}
          </div>

          {/* Quick match summary */}
          <div className="flex flex-wrap gap-x-4 gap-y-1 mt-1.5">
            {rule.match?.topics && rule.match.topics.length > 0 && (
              <span className="flex items-center gap-1 font-mono text-xs text-muted-foreground">
                <Tag className="w-3 h-3 text-cordum/50" />
                {rule.match.topics.join(", ")}
              </span>
            )}
            {rule.match?.risk_tags && rule.match.risk_tags.length > 0 && (
              <span className="flex items-center gap-1 font-mono text-xs text-muted-foreground">
                <AlertTriangle className="w-3 h-3 text-warning/50" />
                {rule.match.risk_tags.join(", ")}
              </span>
            )}
            {rule.match?.capabilities && rule.match.capabilities.length > 0 && (
              <span className="flex items-center gap-1 font-mono text-xs text-muted-foreground">
                <Shield className="w-3 h-3 text-info/50" />
                {rule.match.capabilities.join(", ")}
              </span>
            )}
            {rule.match?.tenants && rule.match.tenants.length > 0 && (
              <span className="flex items-center gap-1 font-mono text-xs text-muted-foreground">
                <Layers className="w-3 h-3 text-cordum/50" />
                {rule.match.tenants.join(", ")}
              </span>
            )}
          </div>

          {rule.reason && (
            <p className="text-xs text-muted-foreground/70 italic mt-1 truncate">
              "{rule.reason}"
            </p>
          )}
        </div>

        {/* Expand chevron */}
        <ChevronDown
          className={cn(
            "w-4 h-4 text-muted-foreground shrink-0 mt-1 transition-transform duration-200",
            expanded && "rotate-180",
          )}
        />
      </button>

      {/* ── Expanded detail ── */}
      {expanded && (
        <div className="border-t border-border/60 px-4 py-4 space-y-4 animate-in fade-in-0 duration-200">
          {/* Description */}
          {rule.description && (
            <p className="text-sm text-muted-foreground leading-relaxed">
              {rule.description}
            </p>
          )}

          {/* Match criteria table */}
          {matchEntries.length > 0 && (
            <div>
              <h4 className="text-xs font-mono text-muted-foreground uppercase tracking-widest mb-2 flex items-center gap-1.5">
                <Shield className="w-3 h-3" />
                Match Criteria
              </h4>
              <div className="bg-surface-2/50 border border-border/40 rounded-xl overflow-hidden">
                <table className="w-full text-xs">
                  <tbody>
                    {matchEntries.map(([key, value]) => (
                      <tr key={key} className="border-b border-border/30 last:border-b-0">
                        <td className="px-3 py-2 font-mono text-muted-foreground w-[140px] align-top">
                          {key}
                        </td>
                        <td className="px-3 py-2 font-mono text-foreground">
                          {formatMatchValue(value)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {/* Velocity config */}
          {rule.velocity && (
            <div>
              <h4 className="text-xs font-mono text-muted-foreground uppercase tracking-widest mb-2 flex items-center gap-1.5">
                <Clock className="w-3 h-3" />
                Velocity Limit
              </h4>
              <div className="flex gap-4 text-xs">
                <div className="bg-[var(--color-warning)]/5 border border-[var(--color-warning)]/20 rounded-xl px-3 py-2">
                  <span className="text-muted-foreground">Max requests</span>
                  <span className="ml-2 font-mono font-semibold text-foreground">{rule.velocity.max_requests}</span>
                </div>
                <div className="bg-[var(--color-warning)]/5 border border-[var(--color-warning)]/20 rounded-xl px-3 py-2">
                  <span className="text-muted-foreground">Window</span>
                  <span className="ml-2 font-mono font-semibold text-foreground">{rule.velocity.window_seconds}s</span>
                </div>
                <div className="bg-[var(--color-warning)]/5 border border-[var(--color-warning)]/20 rounded-xl px-3 py-2">
                  <span className="text-muted-foreground">Key</span>
                  <span className="ml-2 font-mono font-semibold text-foreground">{rule.velocity.key}</span>
                </div>
              </div>
            </div>
          )}

          {/* Allowlist config */}
          {rule.match?.label_allowlist && Object.keys(rule.match.label_allowlist).length > 0 && (
            <div>
              <h4 className="text-xs font-mono text-muted-foreground uppercase tracking-widest mb-2 flex items-center gap-1.5">
                <Shield className="w-3 h-3" />
                Allowlist
              </h4>
              {Object.entries(rule.match.label_allowlist).map(([key, values]) => (
                <div key={key} className="bg-[var(--color-info)]/5 border border-[var(--color-info)]/20 rounded-xl px-3 py-2 text-xs">
                  <span className="text-muted-foreground font-mono">{key}:</span>
                  <div className="flex flex-wrap gap-1 mt-1">
                    {values.map((v) => (
                      <span key={v} className="bg-surface-2 border border-border/40 rounded-xl px-1.5 py-0.5 font-mono text-foreground">
                        {v}
                      </span>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          )}

          {/* Threshold config */}
          {rule.match?.label_threshold && Object.keys(rule.match.label_threshold).length > 0 && (
            <div>
              <h4 className="text-xs font-mono text-muted-foreground uppercase tracking-widest mb-2 flex items-center gap-1.5">
                <Hash className="w-3 h-3" />
                Threshold
              </h4>
              <div className="flex gap-4 text-xs">
                {Object.entries(rule.match.label_threshold).map(([key, maxVal]) => (
                  <div key={key} className="bg-destructive/5 border border-destructive/20 rounded-xl px-3 py-2">
                    <span className="text-muted-foreground font-mono">{key}</span>
                    <span className="ml-2 text-muted-foreground">&gt;</span>
                    <span className="ml-1 font-mono font-semibold text-foreground">{maxVal}</span>
                    <span className="ml-1 text-muted-foreground">triggers deny</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Constraints */}
          {constraintEntries.length > 0 && (
            <div>
              <h4 className="text-xs font-mono text-muted-foreground uppercase tracking-widest mb-2 flex items-center gap-1.5">
                <Network className="w-3 h-3" />
                Constraints
              </h4>
              <div className="bg-surface-2/50 border border-border/40 rounded-xl overflow-hidden">
                <table className="w-full text-xs">
                  <tbody>
                    {constraintEntries.map((entry, i) => (
                      <tr key={i} className="border-b border-border/30 last:border-b-0">
                        <td className="px-3 py-2 font-mono text-muted-foreground/60 w-[80px] align-top text-xs uppercase">
                          {entry.section}
                        </td>
                        <td className="px-3 py-2 font-mono text-muted-foreground w-[140px] align-top">
                          {entry.key}
                        </td>
                        <td className="px-3 py-2 font-mono text-foreground">
                          {entry.value}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {/* Metadata row */}
          <div className="flex flex-wrap gap-x-5 gap-y-1 text-xs font-mono text-muted-foreground">
            <span className="flex items-center gap-1">
              <Hash className="w-3 h-3" />
              Priority: {rule.priority}
            </span>
            {rule.hitCount24h !== undefined && (
              <span className="flex items-center gap-1">
                <Clock className="w-3 h-3" />
                24h evals: {rule.hitCount24h}
              </span>
            )}
            {rule.lastTriggered && (
              <span className="flex items-center gap-1">
                <Clock className="w-3 h-3" />
                Last: {new Date(rule.lastTriggered).toLocaleString()}
              </span>
            )}
            {rule.created_by && (
              <span>Author: {rule.created_by}</span>
            )}
            {rule.version !== undefined && (
              <span>v{rule.version}</span>
            )}
          </div>

          {/* YAML viewer toggle */}
          <div>
            <button
              type="button"
              onClick={() => setShowYaml(!showYaml)}
              className="flex items-center gap-1.5 text-xs font-mono text-cordum hover:text-cordum-bright transition-colors"
            >
              <Code2 className="w-3.5 h-3.5" />
              {showYaml ? "Hide YAML" : "View YAML"}
              {showYaml ? (
                <EyeOff className="w-3 h-3" />
              ) : (
                <Eye className="w-3 h-3" />
              )}
            </button>

            {showYaml && (
              <div className="mt-2 relative">
                <div className="absolute top-2 right-2 z-10">
                  <button
                    type="button"
                    onClick={handleCopy}
                    className="flex items-center gap-1 px-2 py-1 rounded-xl bg-surface-3/80 hover:bg-surface-3 border border-border/40 text-xs font-mono text-muted-foreground hover:text-foreground transition-all"
                  >
                    {copied ? (
                      <>
                        <Check className="w-3 h-3 text-[var(--color-success)]" />
                        Copied
                      </>
                    ) : (
                      <>
                        <Copy className="w-3 h-3" />
                        Copy
                      </>
                    )}
                  </button>
                </div>
                <pre className="bg-surface-2 border border-border/40 rounded-xl p-4 pr-20 text-xs font-mono text-foreground leading-relaxed overflow-x-auto max-h-[400px] overflow-y-auto">
                  <code>{yamlStr}</code>
                </pre>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

