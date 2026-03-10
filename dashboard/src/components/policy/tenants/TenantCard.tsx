import { ArrowRight } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";

export interface TenantSummaryCard {
  id: string;
  label: string;
  allowTopicsCount: number;
  mcpServersCount: number;
  maxConcurrentJobs?: number;
}

interface TenantCardProps {
  tenant: TenantSummaryCard;
  canEdit: boolean;
  onOpen: (tenantId: string) => void;
}

export function TenantCard({ tenant, canEdit, onOpen }: TenantCardProps) {
  return (
    <article className="instrument-card p-4">
      <div className="mb-3 flex items-center justify-between gap-3">
        <div>
          <p className="text-sm font-semibold text-foreground">{tenant.label}</p>
          <p className="text-[11px] text-muted-foreground font-mono">{tenant.id}</p>
        </div>
        <StatusBadge variant={canEdit ? "healthy" : "muted"}>
          {canEdit ? "editable" : "read-only"}
        </StatusBadge>
      </div>

      <dl className="grid grid-cols-3 gap-2 text-[11px]">
        <div className="rounded border border-border/70 bg-surface-1 px-2 py-1.5">
          <dt className="text-muted-foreground">allow_topics</dt>
          <dd className="mt-1 font-mono text-foreground">{tenant.allowTopicsCount}</dd>
        </div>
        <div className="rounded border border-border/70 bg-surface-1 px-2 py-1.5">
          <dt className="text-muted-foreground">mcp servers</dt>
          <dd className="mt-1 font-mono text-foreground">{tenant.mcpServersCount}</dd>
        </div>
        <div className="rounded border border-border/70 bg-surface-1 px-2 py-1.5">
          <dt className="text-muted-foreground">max_concurrent_jobs</dt>
          <dd className="mt-1 font-mono text-foreground">
            {typeof tenant.maxConcurrentJobs === "number" ? tenant.maxConcurrentJobs : "—"}
          </dd>
        </div>
      </dl>

      <div className="mt-3 flex justify-end">
        <Button variant="outline" size="sm" onClick={() => onOpen(tenant.id)}>
          {canEdit ? "Manage tenant" : "View tenant"}
          <ArrowRight className="ml-1 h-3.5 w-3.5" />
        </Button>
      </div>
    </article>
  );
}
