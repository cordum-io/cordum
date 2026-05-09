import { GitBranch } from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { EmptyState } from "@/components/ui/EmptyState";

/**
 * Policy Studio — Bundles surface (foundation shell, epic-d9a6c0a1 Dashboard 1).
 * Dashboard 5 ships the list + detail-with-tabs UI; this is the route shell.
 */
export default function BundlesPage() {
  return (
    <div className="space-y-6">
      <PageHeader
        label="Policy Studio"
        title="Policy Bundles"
        subtitle="Group rules + deploy to scopes"
      />
      <EmptyState
        icon={<GitBranch className="h-5 w-5" />}
        title="Bundles surface coming online"
        description="The bundle list + scope picker ship with Dashboard 5. Bundle storage backend is Backend 2."
      />
    </div>
  );
}
