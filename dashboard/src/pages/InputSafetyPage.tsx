/*
 * DESIGN: "Control Surface" — Input Safety
 * PRD Section 35: Input safety configuration with PII/injection detection
 */
import { useState, useEffect, useMemo } from "react";
import { motion } from "framer-motion";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { AlertTriangle, Save, Plus, Trash2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { toast } from "sonner";
import { usePolicyRules } from "@/hooks/usePolicies";
import { useSetConfig } from "@/hooks/useSettings";
import type { PolicyRule } from "@/api/types";

interface DisplayRule {
  id: string;
  name: string;
  type: "pii" | "injection" | "custom";
  pattern?: string;
  enabled: boolean;
  action: "block" | "warn" | "redact";
}

function deriveType(rule: PolicyRule): "pii" | "injection" | "custom" {
  const name = rule.name.toLowerCase();
  const topics = rule.match?.topics?.join(" ").toLowerCase() ?? "";
  const tags = rule.match?.risk_tags?.join(" ").toLowerCase() ?? "";
  const all = `${name} ${topics} ${tags}`;
  if (all.includes("pii") || all.includes("email") || all.includes("ssn") || all.includes("phone")) return "pii";
  if (all.includes("injection") || all.includes("sql") || all.includes("prompt")) return "injection";
  return "custom";
}

function deriveAction(decision: string): "block" | "warn" | "redact" {
  if (decision === "deny") return "block";
  if (decision === "require_approval") return "warn";
  if (decision === "allow_with_constraints") return "redact";
  return "block";
}

function derivePattern(rule: PolicyRule): string | undefined {
  const topics = rule.match?.topics;
  if (topics && topics.length > 0) return topics.join(", ");
  if (rule.logic) return rule.logic;
  return undefined;
}

function toDisplayRule(rule: PolicyRule): DisplayRule {
  return {
    id: rule.id,
    name: rule.name,
    type: deriveType(rule),
    pattern: derivePattern(rule),
    enabled: rule.enabled,
    action: deriveAction(rule.decision),
  };
}

export default function InputSafetyPage() {
  const [failMode, setFailMode] = useState<"block" | "warn" | "log">("block");
  const [localRules, setLocalRules] = useState<DisplayRule[]>([]);

  const { data: rulesData, isLoading, error } = usePolicyRules();
  const setConfig = useSetConfig();

  const rules = useMemo(() => {
    const items = rulesData?.items ?? [];
    return items.map(toDisplayRule);
  }, [rulesData]);

  // Sync local state when API data loads
  useEffect(() => {
    if (rules.length > 0) {
      setLocalRules(rules);
    }
  }, [rules]);

  const toggleRule = (id: string) => {
    setLocalRules(prev => prev.map(r => r.id === id ? { ...r, enabled: !r.enabled } : r));
  };

  const removeRule = (id: string) => {
    setLocalRules(prev => prev.filter(r => r.id !== id));
  };

  const handleSave = () => {
    setConfig.mutate({
      safety: {
        input: {
          fail_mode: failMode,
          rules: localRules.map(r => ({ id: r.id, enabled: r.enabled })),
        },
      },
    });
  };

  const typeColor = (type: string): BadgeVariant => {
    switch (type) {
      case "pii": return "warning";
      case "injection": return "danger";
      case "custom": return "info";
      default: return "muted";
    }
  };

  const actionColor = (action: string): BadgeVariant => {
    switch (action) {
      case "block": return "danger";
      case "warn": return "warning";
      case "redact": return "info";
      default: return "muted";
    }
  };

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader title="Input Safety" subtitle="Configure input validation and threat detection" actions={<><Button variant="primary" size="sm" onClick={handleSave} loading={setConfig.isPending}>
          <Save className="w-3 h-3 mr-1" />Save Changes
        </Button></>} />

      {isLoading ? (
        <div className="space-y-4">{Array.from({ length: 3 }).map((_, i) => <SkeletonCard key={i} />)}</div>
      ) : error ? (
        <div className="instrument-card p-8 text-center">
          <AlertTriangle className="w-8 h-8 text-red-400 mx-auto mb-3" />
          <p className="text-sm text-foreground font-medium mb-1">Failed to load safety rules</p>
          <p className="text-xs text-muted-foreground">
            {error instanceof Error ? error.message : "An unexpected error occurred"}
          </p>
        </div>
      ) : (
        <>
          {/* Fail Mode */}
          <div className="instrument-card p-5">
            <div className="flex items-center gap-2 mb-3">
              <AlertTriangle className="w-4 h-4 text-amber-400" />
              <span className="text-sm font-display font-semibold text-foreground">Fail Mode</span>
            </div>
            <p className="text-xs text-muted-foreground mb-4">Action when an input safety check fails</p>
            <div className="flex gap-2">
              {(["block", "warn", "log"] as const).map(mode => (
                <button key={mode} onClick={() => setFailMode(mode)}
                  className={cn("px-4 py-2 text-xs font-medium rounded-md border transition-colors capitalize",
                    failMode === mode ? "bg-cordum/10 border-cordum/30 text-cordum" : "border-border text-muted-foreground hover:text-foreground")}>
                  {mode}
                </button>
              ))}
            </div>
          </div>

          {/* Rules */}
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Detection Rules ({localRules.length})</p>
              <Button variant="outline" size="sm" onClick={() => toast.info("Feature coming soon")}>
                <Plus className="w-3 h-3 mr-1" />Add Rule
              </Button>
            </div>
            {localRules.length === 0 ? (
              <div className="instrument-card p-8 text-center">
                <AlertTriangle className="w-6 h-6 text-muted-foreground/30 mx-auto mb-2" />
                <p className="text-sm text-muted-foreground">No input safety rules configured</p>
              </div>
            ) : (
              localRules.map((rule, i) => (
                <motion.div key={rule.id} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: i * 0.03 }}
                  className={cn("instrument-card p-4 flex items-center justify-between", !rule.enabled && "opacity-50")}>
                  <div className="flex items-center gap-3">
                    <button onClick={() => toggleRule(rule.id)}
                      className={cn("w-9 h-5 rounded-full relative transition-colors shrink-0", rule.enabled ? "bg-cordum" : "bg-surface-2")}>
                      <div className={cn("absolute top-0.5 w-4 h-4 rounded-full bg-white transition-transform", rule.enabled ? "left-[18px]" : "left-0.5")} />
                    </button>
                    <div>
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium text-foreground">{rule.name}</span>
                        <StatusBadge variant={typeColor(rule.type)}>{rule.type}</StatusBadge>
                        <StatusBadge variant={actionColor(rule.action)}>{rule.action}</StatusBadge>
                      </div>
                      {rule.pattern && <p className="text-[10px] font-mono text-muted-foreground mt-0.5">{rule.pattern}</p>}
                    </div>
                  </div>
                  {rule.type === "custom" && (
                    <button onClick={() => removeRule(rule.id)} className="p-1.5 rounded hover:bg-red-500/10 transition-colors">
                      <Trash2 className="w-3.5 h-3.5 text-red-400" />
                    </button>
                  )}
                </motion.div>
              ))
            )}
          </div>
        </>
      )}
    </motion.div>
  );
}
