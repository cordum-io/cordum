import { useCallback, useState } from "react";
import { useNavigate } from "react-router-dom";
import { motion } from "framer-motion";
import { PageHeader } from "@/components/layout/PageHeader";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonTable } from "@/components/ui/Skeleton";
import { Search, Plus, Shield, ArrowLeft, ToggleLeft, ToggleRight, Eye } from "lucide-react";
import { usePolicyRules } from "@/hooks/usePolicies";

export default function PoliciesRulesPage() {
  usePageTitle("Policies - Rules");
  const { bundleId } = usePolicyBundleContext();
  const navigate = useNavigate();
  const [ruleType, setRuleType] = useState<"input" | "output">("input");

  const { data: rulesData, isLoading } = usePolicyRules();

  const all = rulesData?.items ?? [];
  const filtered = all.filter((r) => {
    if (!search) return true;
    const q = search.toLowerCase();
    return r.name.toLowerCase().includes(q) || r.id.toLowerCase().includes(q) || (r.reason ?? "").toLowerCase().includes(q);
  });

  return (
    <div className="space-y-4">
      <div className="flex w-fit items-center gap-1 rounded-xl border border-border p-0.5">
        {[
          { id: "input" as const, label: "Input Rules" },
          { id: "output" as const, label: "Output Rules" },
        ].map((item) => (
          <button
            key={item.id}
            type="button"
            onClick={() => setRuleType(item.id)}
            className={cn(
              "rounded-lg px-3 py-1 text-xs font-semibold uppercase tracking-wide transition",
              ruleType === item.id
                ? "bg-accent text-white"
                : "text-muted hover:bg-surface2 hover:text-ink",
            )}
          >
            {item.label}
          </button>
        ))}
      </div>

      {ruleType === "input" ? (
        <RulesTable onSelectRule={handleSelectRule} />
      ) : (
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3 }}
          className="instrument-card overflow-hidden"
        >
          <table className="w-full">
            <thead>
              <tr className="border-b border-border bg-surface-0">
                <th className="px-5 py-3 w-10"></th>
                <th className="text-center px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider w-20">Priority</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Rule Name</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider w-24">Decision</th>
                <th className="text-right px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider w-28">Updated</th>
                <th className="px-5 py-3 w-10"></th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((r) => (
                <tr
                  key={r.id}
                  onClick={() => navigate(`/policies/rules/${r.id}`)}
                  className="border-b border-border hover:bg-surface-1 transition-colors cursor-pointer"
                >
                  <td className="px-5 py-3">
                    {r.enabled !== false
                      ? <ToggleRight className="w-4 h-4 text-cordum" />
                      : <ToggleLeft className="w-4 h-4 text-muted-foreground" />
                    }
                  </td>
                  <td className="px-5 py-3 text-center font-mono text-xs text-muted-foreground">{r.priority ?? "—"}</td>
                  <td className="px-5 py-3">
                    <div>
                      <p className="text-sm font-medium text-foreground">{r.name}</p>
                      {r.reason && <p className="text-xs text-muted-foreground truncate max-w-[300px]">{r.reason}</p>}
                    </div>
                  </td>
                  <td className="px-5 py-3">
                    <StatusBadge variant={r.decision === "allow" ? "healthy" : r.decision === "deny" ? "danger" : "warning"}>
                      {r.decision}
                    </StatusBadge>
                  </td>
                  <td className="px-5 py-3 text-right text-xs text-muted-foreground font-mono">
                    {r.updated_at ? new Date(r.updated_at).toLocaleDateString() : "—"}
                  </td>
                  <td className="px-5 py-3">
                    <button className="p-1 rounded hover:bg-surface-2 transition-colors" aria-label="View details">
                      <Eye className="w-3.5 h-3.5 text-muted-foreground" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </motion.div>
      )}
    </div>
  );
}
