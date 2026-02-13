import { useState, useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { usePolicyBundleContext } from "../components/policy/PolicyBundleContext";
import { PolicySimulator } from "../components/policy/PolicySimulator";
import type { SimulatorMode } from "../components/policy/PolicySimulator";
import { PolicyReplay } from "../components/policy/PolicyReplay";
import { BatchSimulator } from "../components/policy/BatchSimulator";
import { cn } from "../lib/utils";
import { usePageTitle } from "../hooks/usePageTitle";

type SimTab = "single" | "explain" | "batch";

export default function PoliciesSimulatorPage() {
  usePageTitle("Policies - Simulator");
  const { bundleId } = usePolicyBundleContext();
  const [searchParams] = useSearchParams();
  const [tab, setTab] = useState<SimTab>("single");

  const initialCapabilities = useMemo(() => {
    const raw = searchParams.get("caps");
    return raw ? raw.split(",").filter(Boolean) : undefined;
  }, [searchParams]);

  const initialRiskTags = useMemo(() => {
    const raw = searchParams.get("tags");
    return raw ? raw.split(",").filter(Boolean) : undefined;
  }, [searchParams]);

  if (!bundleId) {
    return (
      <div className="rounded-2xl border border-dashed border-border px-6 py-12 text-center text-sm text-muted">
        No policy bundles found. Create one to simulate policy checks.
      </div>
    );
  }

  const simulatorMode: SimulatorMode = tab === "explain" ? "explain" : "simulate";

  return (
    <div className="space-y-6">
      {/* Tab toggle */}
      <div className="flex gap-1 rounded-full border border-border p-1 w-fit">
        {([
          { key: "single" as const, label: "Single Test" },
          { key: "explain" as const, label: "Explain" },
          { key: "batch" as const, label: "Batch Test" },
        ]).map(({ key, label }) => (
          <button
            key={key}
            type="button"
            onClick={() => setTab(key)}
            className={cn(
              "rounded-full px-5 py-2 text-xs font-semibold uppercase tracking-widest transition",
              tab === key
                ? "bg-accent/15 text-accent"
                : "text-muted hover:text-ink",
            )}
          >
            {label}
          </button>
        ))}
      </div>

      {/* Tab content */}
      {tab === "batch" ? (
        <BatchSimulator bundleId={bundleId} />
      ) : (
        <div className="space-y-8">
          <PolicySimulator
            bundleId={bundleId}
            mode={simulatorMode}
            initialCapabilities={initialCapabilities}
            initialRiskTags={initialRiskTags}
          />
          {tab === "single" && <PolicyReplay bundleId={bundleId} />}
        </div>
      )}
    </div>
  );
}
