import { PageHeader } from "@/components/layout/PageHeader";
import { EmptyState } from "@/components/ui/EmptyState";
import { ShieldCheck } from "lucide-react";

export default function SafetyControlsPage() {
  return (
    <div className="space-y-6">
      <PageHeader
        label="Security"
        title="Safety Controls"
        subtitle="Manage Input and Output safety scanners and kernel configuration"
      />
      
      <EmptyState
        icon={<ShieldCheck className="h-6 w-6" />}
        title="Safety Controls Hub"
        description="Consolidated safety controls for both Input and Output pipelines are being moved here. Visit Settings > System Health to check kernel status for now."
      />
    </div>
  );
}
