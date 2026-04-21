import { KeyRound } from "lucide-react";
import { Button } from "@/components/ui/Button";
import {
  InstrumentCard,
  InstrumentCardBody,
  InstrumentCardFooter,
  InstrumentCardHeader,
} from "@/components/ui/InstrumentCard";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { CollapsibleSection } from "@/components/ui/CollapsibleSection";
import type { DelegationView } from "@/api/types";
import { cn } from "@/lib/utils";

export interface DelegationChainNodeProps {
  delegation: DelegationView;
  agentId: string;
  depth: number;
  status: DelegationNodeStatus;
  riskTier?: string;
  expiresLabel: string;
  isLeaf: boolean;
  onNavigate: (agentId: string) => void;
  onRequestRevoke?: () => void;
  revokeDescriptionId?: string;
  revokeDescription?: string;
}

export type DelegationNodeStatus = "active" | "revoked" | "expired";

export function DelegationChainNode({
  delegation,
  agentId,
  depth,
  status,
  riskTier,
  expiresLabel,
  isLeaf,
  onNavigate,
  onRequestRevoke,
  revokeDescriptionId,
  revokeDescription,
}: DelegationChainNodeProps) {
  const statusVariant = delegationStatusVariant(status);
  const accent = delegationAccent(status);
  const scopeSummary = summarizeScopeLabel(delegation.allowedActions);

  return (
    <div data-testid={`delegation-node-${agentId}`}>
      {depth > 0 && (
        <div
          aria-hidden="true"
          className="mb-2 flex items-center gap-1.5 text-[10px] uppercase tracking-[0.18em] text-muted-foreground sm:hidden"
          data-testid={`delegation-depth-marker-${agentId}`}
        >
          {Array.from({ length: depth }).map((_, index) => (
            <span
              key={`${agentId}-depth-${index}`}
              className="h-1.5 w-1.5 rounded-full bg-border"
            />
          ))}
          <span>Hop {depth}</span>
        </div>
      )}
      <InstrumentCard accent={accent} className="overflow-hidden p-5">
        <InstrumentCardBody className="space-y-4">
          <InstrumentCardHeader
            title={agentId}
            subtitle={expiresLabel}
            action={
              <div className="flex flex-wrap items-center justify-end gap-2">
                <span
                  aria-label={`Delegation status: ${status}`}
                  data-testid={`delegation-status-${agentId}`}
                >
                  <StatusBadge variant={statusVariant}>{status}</StatusBadge>
                </span>
                {riskTier && (
                  <StatusBadge variant={riskTierVariant(riskTier)}>
                    {riskTier}
                  </StatusBadge>
                )}
              </div>
            }
          />

          <button
            type="button"
            onClick={() => onNavigate(agentId)}
            className={cn(
              "flex w-full items-center justify-between rounded-2xl border border-border/60 bg-surface-2/40 px-4 py-3 text-left transition-colors hover:bg-surface-2",
            )}
            data-testid={`delegation-node-nav-${agentId}`}
          >
            <div>
              <p className="text-xs font-medium uppercase tracking-[0.18em] text-muted-foreground">
                Delegator
              </p>
              <p className="mt-1 text-sm font-medium text-foreground">
                Open agent detail
              </p>
            </div>
            <span className="text-xs text-muted-foreground">/agents/{agentId}</span>
          </button>

          <CollapsibleSection
            title="Delegated scope"
            description={`${delegation.allowedTopics.length} topic${delegation.allowedTopics.length === 1 ? "" : "s"}`}
            defaultOpen={false}
            leading={<KeyRound className="h-4 w-4 text-cordum" />}
            badge={
              <span className="text-[11px] text-muted-foreground">
                {scopeSummary.visible.length} action{scopeSummary.visible.length === 1 ? "" : "s"}
              </span>
            }
          >
            <div className="space-y-3 rounded-2xl border border-border/50 bg-surface-2/30 p-3">
              <div>
                <p className="text-[11px] font-semibold uppercase tracking-[0.16em] text-muted-foreground">
                  Allowed actions
                </p>
                <div className="mt-2 flex flex-wrap gap-2">
                  {delegation.allowedActions.length > 0 ? (
                    delegation.allowedActions.map((action) => (
                      <StatusBadge key={action} variant="muted">
                        {action}
                      </StatusBadge>
                    ))
                  ) : (
                    <span className="text-xs text-muted-foreground">No scoped actions</span>
                  )}
                </div>
              </div>

              <div>
                <p className="text-[11px] font-semibold uppercase tracking-[0.16em] text-muted-foreground">
                  Allowed topics
                </p>
                <div className="mt-2 flex flex-wrap gap-2">
                  {delegation.allowedTopics.length > 0 ? (
                    delegation.allowedTopics.map((topic) => (
                      <StatusBadge key={topic} variant="muted">
                        {topic}
                      </StatusBadge>
                    ))
                  ) : (
                    <span className="text-xs text-muted-foreground">All tenant topics</span>
                  )}
                </div>
              </div>
            </div>
          </CollapsibleSection>
        </InstrumentCardBody>

        {isLeaf && onRequestRevoke && (
          <InstrumentCardFooter className="flex items-center justify-between gap-3">
            <div>
              <p className="text-xs font-medium text-foreground">Active token</p>
              <p className="mt-1 text-xs text-muted-foreground">
                Revoking this token also blocks downstream extensions.
              </p>
            </div>
            {revokeDescriptionId && revokeDescription ? (
              <span id={revokeDescriptionId} className="sr-only">
                {revokeDescription}
              </span>
            ) : null}
            <Button
              variant="danger"
              size="sm"
              onClick={onRequestRevoke}
              data-testid="delegation-revoke-trigger"
              aria-describedby={revokeDescriptionId}
            >
              Revoke
            </Button>
          </InstrumentCardFooter>
        )}
      </InstrumentCard>
    </div>
  );
}

export interface ScopeSummary {
  visible: string[];
  overflow: number;
}

export function summarizeScopeLabel(actions: string[]): ScopeSummary {
  const unique = Array.from(new Set(actions.filter(Boolean))).sort((a, b) =>
    a.localeCompare(b),
  );
  return {
    visible: unique.slice(0, 3),
    overflow: Math.max(0, unique.length - 3),
  };
}

export function delegationStatusVariant(status: DelegationNodeStatus): BadgeVariant {
  switch (status) {
    case "revoked":
      return "danger";
    case "expired":
      return "warning";
    default:
      return "healthy";
  }
}

function delegationAccent(status: DelegationNodeStatus) {
  switch (status) {
    case "revoked":
      return "danger" as const;
    case "expired":
      return "warning" as const;
    default:
      return "cordum" as const;
  }
}

function riskTierVariant(riskTier: string): BadgeVariant {
  switch (riskTier.trim().toLowerCase()) {
    case "critical":
    case "high":
      return "danger";
    case "medium":
      return "warning";
    case "low":
      return "healthy";
    default:
      return "muted";
  }
}
