import { useMemo } from "react";
import { ShieldAlert } from "lucide-react";
import { DelegationChainViz } from "@/components/delegations/DelegationChainViz";
import { LegacyDataTable } from "@/components/ui/LegacyDataTable";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { SkeletonCard, SkeletonTable } from "@/components/ui/Skeleton";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { useAgentDelegations, useAllDelegations } from "@/hooks/useDelegations";
import type { DelegationView } from "@/api/types";
import {
  countCascadeDescendants,
  formatDelegationExpiry,
  getDelegationNodeStatus,
} from "./DelegationChainViz";

interface AgentDelegationsPanelProps {
  agentId: string;
}

export function AgentDelegationsPanel({ agentId }: AgentDelegationsPanelProps) {
  const outboundQuery = useAgentDelegations(agentId, { limit: 100 });
  const inboundQuery = useAllDelegations({ limit: 200 });

  const outbound = useMemo(
    () => flattenDelegations(outboundQuery.data?.pages),
    [outboundQuery.data?.pages],
  );
  const allDelegations = useMemo(
    () => flattenDelegations(inboundQuery.data?.pages),
    [inboundQuery.data?.pages],
  );
  const inbound = useMemo(
    () =>
      allDelegations.filter(
        (delegation) => delegation.audience === agentId,
      ),
    [agentId, allDelegations],
  );
  const activeOutbound = useMemo(
    () =>
      outbound.filter(
        (delegation) => getDelegationNodeStatus(delegation) === "active",
      ),
    [outbound],
  );

  if (outboundQuery.isLoading || inboundQuery.isLoading) {
    return (
      <div className="space-y-4">
        <SkeletonCard />
        <SkeletonTable rows={3} />
      </div>
    );
  }

  if (outboundQuery.isError || inboundQuery.isError) {
    return (
      <ErrorBanner
        title="Failed to load delegations"
        message={
          outboundQuery.error?.message ??
          inboundQuery.error?.message ??
          "An unexpected error occurred"
        }
        onRetry={() => {
          void outboundQuery.refetch();
          void inboundQuery.refetch();
        }}
      />
    );
  }

  if (activeOutbound.length === 0 && inbound.length === 0) {
    return (
      <EmptyState
        icon={<ShieldAlert className="h-5 w-5" />}
        title="No delegations yet"
        description="This agent has not issued or received any scoped delegation tokens."
      />
    );
  }

  return (
    <div className="space-y-6">
      <section className="space-y-4">
        <div>
          <h2 className="text-sm font-semibold text-foreground">
            Active outbound delegations
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">
            Tokens this agent has issued to downstream agents.
          </p>
        </div>

        {activeOutbound.length === 0 ? (
          <EmptyState
            title="No active outbound delegations"
            description="Scoped tokens issued by this agent will appear here."
          />
        ) : (
          <div className="space-y-4">
            {activeOutbound.map((delegation) => (
              <DelegationChainViz
                key={delegation.jti}
                delegation={delegation}
                loadCascadeCount={async (jti) =>
                  countCascadeDescendants(allDelegations, jti)}
              />
            ))}
          </div>
        )}
      </section>

      <section className="space-y-4">
        <div>
          <h2 className="text-sm font-semibold text-foreground">
            Inbound delegations
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">
            Read-only view of tokens currently targeting this agent.
          </p>
        </div>

        <LegacyDataTable
          compact
          data={inbound}
          keyExtractor={(row) => row.jti}
          emptyMessage="No inbound delegations"
          columns={[
            {
              key: "issuer",
              header: "Issuer",
              render: (row) => (
                <div>
                  <div className="text-sm font-medium text-foreground">{row.issuer}</div>
                  <div className="text-xs text-muted-foreground">{row.subject}</div>
                </div>
              ),
            },
            {
              key: "scope",
              header: "Scope",
              render: (row) => row.allowedActions.slice(0, 3).join(", ") || "all actions",
            },
            {
              key: "status",
              header: "Status",
              render: (row) => (
                <StatusBadge
                  variant={
                    getDelegationNodeStatus(row) === "revoked"
                      ? "danger"
                      : getDelegationNodeStatus(row) === "expired"
                        ? "warning"
                        : "healthy"
                  }
                >
                  {getDelegationNodeStatus(row)}
                </StatusBadge>
              ),
            },
            {
              key: "expires",
              header: "Expires",
              render: (row) =>
                formatDelegationExpiry(
                  row.expiresAt,
                  getDelegationNodeStatus(row),
                ),
            },
          ]}
        />
      </section>
    </div>
  );
}

function flattenDelegations(
  pages: Array<{ items: DelegationView[] }> | undefined,
): DelegationView[] {
  return pages?.flatMap((page) => page.items) ?? [];
}
