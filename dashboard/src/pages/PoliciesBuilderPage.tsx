import { useState } from "react";
import { usePolicyBundleContext } from "../components/policy/PolicyBundleContext";
import { PolicyYamlEditor } from "../components/policy/PolicyYamlEditor";
import { VisualRuleBuilder } from "../components/policy/VisualRuleBuilder";
import { cn } from "../lib/utils";
import { usePageTitle } from "../hooks/usePageTitle";

type BuilderTab = "visual" | "yaml";

export default function PoliciesBuilderPage() {
  usePageTitle("Policies - Builder");
  const { bundleId } = usePolicyBundleContext();
  const [tab, setTab] = useState<BuilderTab>("visual");

  if (!bundleId) {
    return (
      <div className="rounded-2xl border border-dashed border-border px-6 py-12 text-center text-sm text-muted">
        No policy bundles found. Create one to get started.
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {/* Tab toggle */}
      <div className="flex gap-1 rounded-xl border border-border bg-surface2/40 p-1 w-fit">
        {(["visual", "yaml"] as const).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            className={cn(
              "rounded-lg px-4 py-1.5 text-xs font-semibold transition",
              tab === t
                ? "bg-surface1 text-ink shadow-sm"
                : "text-muted hover:text-ink",
            )}
          >
            {t === "visual" ? "Visual" : "YAML"}
          </button>
        ))}
      </div>

      {/* Tab content */}
      {tab === "visual" ? (
        <VisualRuleBuilder bundleId={bundleId} />
      ) : (
        <PolicyYamlEditor bundleId={bundleId} />
      )}
    </div>
  );
}
