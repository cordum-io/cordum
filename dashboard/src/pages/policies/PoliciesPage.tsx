import { Shield } from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { EmptyState } from "@/components/ui/EmptyState";

/**
 * Policy Studio — Rules surface (foundation shell, epic-d9a6c0a1 Dashboard 1).
 * Dashboard 2 fills this with the rules table; until then the page renders
 * a designed empty state so /policies is functional + bookmarkable.
 */
export default function PoliciesPage() {
  return (
    <div className="space-y-6">
      <PageHeader
        label="Policy Studio"
        title="Policy Rules"
        subtitle="Author and manage rules across job + edge surfaces"
      />
      <EmptyState
        icon={<Shield className="h-5 w-5" />}
        title="Rules surface coming online"
        description="The unified Rules table ships with Dashboard 2. Existing rules continue to evaluate via /govern/overview during the transition."
      />
    </div>
  );
}
