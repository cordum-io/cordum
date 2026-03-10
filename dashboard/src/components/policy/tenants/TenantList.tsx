import { Building2 } from "lucide-react";
import { EmptyState } from "@/components/ui/EmptyState";
import { TenantCard, type TenantSummaryCard } from "./TenantCard";

export type { TenantSummaryCard } from "./TenantCard";

interface TenantListProps {
  tenants: TenantSummaryCard[];
  canEdit: boolean;
  onOpenTenant: (tenantId: string) => void;
}

export function TenantList({ tenants, canEdit, onOpenTenant }: TenantListProps) {
  if (tenants.length === 0) {
    return (
      <EmptyState
        icon={<Building2 className="h-6 w-6" />}
        title="No tenant policy entries found"
        description="This bundle has no tenants map yet. Add tenant entries in policy YAML to manage access boundaries."
      />
    );
  }

  return (
    <section className="space-y-3">
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {tenants.map((tenant) => (
          <TenantCard
            key={tenant.id}
            tenant={tenant}
            canEdit={canEdit}
            onOpen={onOpenTenant}
          />
        ))}
      </div>
    </section>
  );
}
