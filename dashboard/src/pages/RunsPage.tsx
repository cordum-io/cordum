import { PageHeader } from "@/components/layout/PageHeader";
import { EmptyState } from "@/components/ui/EmptyState";
import { ListChecks } from "lucide-react";

export default function RunsPage() {
  return (
    <div className="space-y-6">
      <PageHeader
        label="Operations"
        title="Workflow Runs"
        subtitle="Monitor multi-step job executions across your fleet"
      />
      
      <EmptyState
        icon={<ListChecks className="h-6 w-6" />}
        title="Runs Dashboard"
        description="Unified run monitoring for all workflows is coming in a follow-up. View runs for specific workflows via the Workflows page today."
      />
    </div>
  );
}
