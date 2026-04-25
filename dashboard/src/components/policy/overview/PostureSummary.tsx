import { useMemo } from "react";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { MetricValue } from "@/components/ui/MetricValue";
import {
  Package,
  Shield,
  ShieldAlert,
  UserCheck,
  FileWarning,
} from "lucide-react";
import type { PolicyBundle, PolicyRule } from "@/api/types";

interface PostureSummaryProps {
  bundles: PolicyBundle[];
  allRules: PolicyRule[];
}

export function PostureSummary({ bundles, allRules }: PostureSummaryProps) {
  const stats = useMemo(() => {
    const activeBundles = bundles.filter(
      (b) => b.status !== "archived" && b.enabled !== false,
    );
    const draftBundles = bundles.filter((b) => b.status === "draft");
    const enabledRules = allRules.filter((r) => r.enabled !== false);
    const denyRules = enabledRules.filter((r) => r.decision === "deny");
    const approvalRules = enabledRules.filter(
      (r) => r.decision === "require_approval",
    );

    return {
      activeBundles: activeBundles.length,
      totalBundles: bundles.length,
      activeRules: enabledRules.length,
      totalRules: allRules.length,
      denyCount: denyRules.length,
      approvalCount: approvalRules.length,
      draftCount: draftBundles.length,
    };
  }, [bundles, allRules]);

  return (
    <div>
      <span className="text-xs font-mono text-muted-foreground uppercase tracking-widest block mb-3">
        Posture Summary
      </span>
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3">
        <InstrumentCard accent="cordum">
          <MetricValue
            value={stats.activeBundles}
            label="Bundles Active"
            size="sm"
            icon={<Package className="w-4 h-4" />}
          />
        </InstrumentCard>

        <InstrumentCard accent="cordum">
          <MetricValue
            value={stats.activeRules}
            label="Rules Active"
            size="sm"
            icon={<Shield className="w-4 h-4" />}
          />
        </InstrumentCard>

        <InstrumentCard accent={stats.denyCount > 0 ? "governance" : "muted"}>
          <MetricValue
            value={stats.denyCount}
            label="Deny Rules"
            size="sm"
            icon={<ShieldAlert className="w-4 h-4" />}
          />
        </InstrumentCard>

        <InstrumentCard accent={stats.approvalCount > 0 ? "warning" : "muted"}>
          <MetricValue
            value={stats.approvalCount}
            label="Require Approval"
            size="sm"
            icon={<UserCheck className="w-4 h-4" />}
          />
        </InstrumentCard>

        <InstrumentCard accent={stats.draftCount > 0 ? "info" : "muted"}>
          <MetricValue
            value={stats.draftCount}
            label="Draft Bundles"
            size="sm"
            icon={<FileWarning className="w-4 h-4" />}
          />
        </InstrumentCard>
      </div>
    </div>
  );
}
