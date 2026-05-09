import { History } from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { EmptyState } from "@/components/ui/EmptyState";

/**
 * Policy Studio — Decisions surface (foundation shell, epic-d9a6c0a1 Dashboard 1).
 * Dashboard 8/9 ship the live stream + replay/what-if; this is the route shell.
 */
export default function DecisionsPage() {
  return (
    <div className="space-y-6">
      <PageHeader
        label="Policy Studio"
        title="Policy Decisions"
        subtitle="Live stream of policy outcomes"
      />
      <EmptyState
        icon={<History className="h-5 w-5" />}
        title="Decisions stream coming online"
        description="The unified job + edge decisions stream ships with Dashboard 8. Replay + what-if ship with Dashboard 9."
      />
    </div>
  );
}
