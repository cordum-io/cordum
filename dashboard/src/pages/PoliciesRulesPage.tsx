import { useCallback, useState } from "react";
import { useNavigate } from "react-router-dom";
import { RulesTable } from "../components/policy/RulesTable";
import { OutputRulesTab } from "../components/policy/OutputRulesTab";
import type { PolicyRule } from "../api/types";
import { usePageTitle } from "../hooks/usePageTitle";
import { cn } from "../lib/utils";
import { usePolicyBundleContext } from "../components/policy/PolicyBundleContext";

export default function PoliciesRulesPage() {
  usePageTitle("Policies - Rules");
  const { bundleId } = usePolicyBundleContext();
  const navigate = useNavigate();
  const [ruleType, setRuleType] = useState<"input" | "output">("input");

  const handleSelectRule = useCallback(
    (rule: PolicyRule) => {
      const source = rule.source as Record<string, unknown> | undefined;
      const bundleId =
        source && typeof source === "object" && "fragment_id" in source
          ? String(source.fragment_id ?? "").trim()
          : "";
      if (bundleId) {
        navigate(`/policies/rules/new?bundle=${encodeURIComponent(bundleId)}`);
      } else {
        navigate("/policies/rules/new");
      }
    },
    [navigate],
  );

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
              "rounded-2xl px-3 py-1 text-xs font-semibold uppercase tracking-wide transition",
              ruleType === item.id
                ? "bg-accent text-primary-foreground"
                : "text-muted-foreground hover:bg-surface2 hover:text-ink",
            )}
          >
            {item.label}
          </button>
        ))}
      </div>

      {ruleType === "input" ? (
        <RulesTable onSelectRule={handleSelectRule} />
      ) : (
        <OutputRulesTab activeBundleId={bundleId} />
      )}
    </div>
  );
}
