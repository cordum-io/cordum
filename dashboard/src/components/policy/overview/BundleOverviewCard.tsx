import { useState, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { cn } from "@/lib/utils";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";
import { RuleDetailRow } from "./RuleDetailRow";
import {
  ChevronDown,
  Code2,
  Zap,
  Settings,
  Copy,
  Check,
  Tag,
  Clock,
  FileText,
} from "lucide-react";
import type { PolicyBundle, PolicyRule, SafetyDecisionType } from "@/api/types";
import { encodePolicyBundleId } from "@/hooks/usePolicies";

/* ── helpers ── */

function bundleStatusBadge(bundle: PolicyBundle): { variant: BadgeVariant; label: string; dot: boolean } {
  if (bundle.status === "draft") return { variant: "warning", label: "Draft", dot: true };
  if (bundle.status === "archived") return { variant: "muted", label: "Archived", dot: true };
  if (bundle.enabled === false) return { variant: "muted", label: "Disabled", dot: true };
  return { variant: "healthy", label: "Published", dot: true };
}

function bundleActiveBadge(bundle: PolicyBundle): { variant: BadgeVariant; label: string } | null {
  if (bundle.status === "draft" || bundle.status === "archived") return null;
  if (bundle.enabled === false) return null;
  return { variant: "cordum", label: "Active" };
}

type DecisionSummary = { decision: SafetyDecisionType; count: number };

function summarizeDecisions(rules: PolicyRule[]): DecisionSummary[] {
  const map = new Map<SafetyDecisionType, number>();
  for (const r of rules) {
    if (!r.decision) continue;
    map.set(r.decision, (map.get(r.decision) || 0) + 1);
  }
  return Array.from(map.entries())
    .map(([decision, count]) => ({ decision, count }))
    .sort((a, b) => b.count - a.count);
}

function collectTopics(rules: PolicyRule[]): string[] {
  const set = new Set<string>();
  for (const r of rules) {
    if (r.match?.topics) {
      for (const t of r.match.topics) set.add(t);
    }
  }
  return Array.from(set);
}

/* ── component ── */

interface BundleOverviewCardProps {
  bundle: PolicyBundle;
  className?: string;
  defaultExpanded?: boolean;
  filterText?: string;
}

export function BundleOverviewCard({
  bundle,
  className,
  defaultExpanded = false,
  filterText,
}: BundleOverviewCardProps) {
  const navigate = useNavigate();
  const [expanded, setExpanded] = useState(defaultExpanded);
  const [showBundleYaml, setShowBundleYaml] = useState(false);
  const [copied, setCopied] = useState(false);

  const rules = bundle.rules ?? [];
  const enabledRules = rules.filter((r) => r.enabled !== false);
  const decisions = useMemo(() => summarizeDecisions(enabledRules), [enabledRules]);
  const topics = useMemo(() => collectTopics(rules), [rules]);
  const status = bundleStatusBadge(bundle);
  const active = bundleActiveBadge(bundle);

  const accentClass =
    bundle.status === "draft"
      ? "status-warning"
      : bundle.enabled === false || bundle.status === "archived"
        ? "status-muted"
        : "";

  const filteredRules = useMemo(() => {
    if (!filterText) return rules;
    const lower = filterText.toLowerCase();
    return rules.filter(
      (r) =>
        r.name.toLowerCase().includes(lower) ||
        r.id.toLowerCase().includes(lower) ||
        r.decision?.toLowerCase().includes(lower) ||
        r.match?.topics?.some((t) => t.toLowerCase().includes(lower)) ||
        r.match?.capabilities?.some((c) => c.toLowerCase().includes(lower)) ||
        r.match?.tenants?.some((t) => t.toLowerCase().includes(lower)) ||
        r.match?.risk_tags?.some((t) => t.toLowerCase().includes(lower)),
    );
  }, [rules, filterText]);

  const handleCopyYaml = async () => {
    if (bundle.content) {
      await navigator.clipboard.writeText(bundle.content);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  return (
    <div
      className={cn(
        "instrument-card !p-0 overflow-hidden transition-all duration-300",
        accentClass,
        expanded && "ring-1 ring-cordum/20",
        className,
      )}
    >
      {/* ── Header (always visible) ── */}
      <button
        type="button"
        onClick={() => setExpanded(!expanded)}
        className="w-full text-left px-5 pt-5 pb-4"
      >
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <h3 className="text-base font-display font-semibold text-foreground tracking-tight">
              {bundle.name}
            </h3>
            <span className="text-xs font-mono text-muted-foreground mt-0.5 block">
              {bundle.id}
              {bundle.version !== undefined && ` v${bundle.version}`}
            </span>
          </div>
          <div className="flex items-center gap-2 shrink-0">
            <StatusBadge variant={status.variant} dot={status.dot}>
              {status.label}
            </StatusBadge>
            {active && (
              <StatusBadge variant={active.variant}>
                {active.label}
              </StatusBadge>
            )}
          </div>
        </div>

        {/* Impact strip */}
        <div className="mt-3 flex flex-wrap items-center gap-2 px-3 py-2.5 bg-surface-2/50 border border-border/40 rounded-xl text-xs">
          <span className="font-mono text-muted-foreground">
            <strong className="text-foreground">{enabledRules.length}</strong>{" "}
            rule{enabledRules.length !== 1 ? "s" : ""}
            {rules.length !== enabledRules.length && (
              <span className="text-muted-foreground/50 ml-1">
                ({rules.length - enabledRules.length} disabled)
              </span>
            )}
          </span>

          <span className="w-px h-4 bg-border" />

          {topics.length > 0 && (
            <>
              <span className="flex items-center gap-1 font-mono text-muted-foreground">
                <Tag className="w-3 h-3 text-cordum/50" />
                <strong className="text-foreground">{topics.slice(0, 3).join(", ")}</strong>
                {topics.length > 3 && (
                  <span className="text-muted-foreground/50">+{topics.length - 3}</span>
                )}
              </span>
              <span className="w-px h-4 bg-border" />
            </>
          )}

          {decisions.map((d) => (
            <SafetyDecisionBadge key={d.decision} decision={d.decision} />
          ))}

          {bundle.eval_count_24h !== undefined && bundle.eval_count_24h > 0 && (
            <>
              <span className="w-px h-4 bg-border" />
              <span className="flex items-center gap-1 font-mono text-muted-foreground">
                <Clock className="w-3 h-3" />
                24h: <strong className="text-foreground">{bundle.eval_count_24h.toLocaleString()}</strong>
              </span>
            </>
          )}
        </div>
      </button>

      {/* ── Expand toggle bar ── */}
      <div className="flex items-center justify-between px-5 py-2.5 border-t border-border/40">
        <button
          type="button"
          onClick={() => setExpanded(!expanded)}
          className="flex items-center gap-1.5 text-xs font-mono text-muted-foreground hover:text-cordum transition-colors"
        >
          <ChevronDown
            className={cn(
              "w-3.5 h-3.5 transition-transform duration-200",
              expanded && "rotate-180",
            )}
          />
          {expanded ? "Hide rules" : `Show ${filteredRules.length} rules`}
        </button>

        <div className="flex items-center gap-1.5">
          {bundle.content && (
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                setShowBundleYaml(!showBundleYaml);
                if (!expanded) setExpanded(true);
              }}
              className="flex items-center gap-1 px-2.5 py-1 rounded-full text-xs font-mono border border-border text-muted-foreground hover:border-cordum hover:text-foreground transition-all"
            >
              <Code2 className="w-3 h-3" />
              {showBundleYaml ? "Hide" : "YAML"}
            </button>
          )}
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              navigate(`/govern/overview?tab=simulator`);
            }}
            className="flex items-center gap-1 px-2.5 py-1 rounded-full text-xs font-mono border border-border text-muted-foreground hover:border-cordum hover:text-foreground transition-all"
          >
            <Zap className="w-3 h-3" />
            Simulate
          </button>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              navigate(`/govern/bundles/${encodePolicyBundleId(bundle.id)}`);
            }}
            className="flex items-center gap-1 px-2.5 py-1 rounded-full text-xs font-sans font-medium bg-cordum text-[var(--primary-foreground)] hover:opacity-85 transition-all"
          >
            <Settings className="w-3 h-3" />
            Manage
          </button>
        </div>
      </div>

      {/* ── Expanded content ── */}
      {expanded && (
        <div className="border-t border-border/40 animate-in fade-in-0 slide-in-from-top-1 duration-200">
          {/* Bundle-level YAML viewer */}
          {showBundleYaml && bundle.content && (
            <div className="px-5 pt-4 pb-2">
              <div className="flex items-center justify-between mb-2">
                <h4 className="text-xs font-mono text-muted-foreground uppercase tracking-widest flex items-center gap-1.5">
                  <FileText className="w-3 h-3" />
                  Bundle YAML
                </h4>
                <button
                  type="button"
                  onClick={handleCopyYaml}
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
              <pre className="bg-surface-2 border border-border/40 rounded-xl p-4 text-xs font-mono text-foreground leading-relaxed overflow-x-auto max-h-[500px] overflow-y-auto">
                <code>{bundle.content}</code>
              </pre>
            </div>
          )}

          {/* Rules list */}
          <div className="px-5 py-4 space-y-2">
            {filteredRules.length === 0 ? (
              <p className="text-sm text-muted-foreground text-center py-6">
                {filterText
                  ? "No rules match the current filter."
                  : "This bundle has no rules."}
              </p>
            ) : (
              filteredRules.map((rule, i) => (
                <RuleDetailRow key={rule.id || i} rule={rule} index={i} />
              ))
            )}
          </div>
        </div>
      )}
    </div>
  );
}

