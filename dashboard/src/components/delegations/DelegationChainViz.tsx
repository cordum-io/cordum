import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowDown, ArrowRight, KeyRound } from "lucide-react";
import type { DelegationView } from "@/api/types";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { useMotionConfig } from "@/hooks/useMotionConfig";
import { useRevokeDelegation } from "@/hooks/useDelegations";
import { cn, formatRelativeTime } from "@/lib/utils";
import {
  DelegationChainNode,
  summarizeScopeLabel,
  type DelegationNodeStatus,
} from "./DelegationChainNode";
import { RevokeDelegationDialog } from "./RevokeDelegationDialog";

export interface DelegationChainVizProps {
  delegation: DelegationView;
  riskTierByAgent?: Record<string, string>;
  loadCascadeCount?: (jti: string) => Promise<number>;
}

interface ChainNode {
  agentId: string;
  status: DelegationNodeStatus;
  expiresLabel: string;
}

export function DelegationChainViz({
  delegation,
  riskTierByAgent,
  loadCascadeCount,
}: DelegationChainVizProps) {
  const navigate = useNavigate();
  const { shouldAnimate } = useMotionConfig();
  const revokeDelegation = useRevokeDelegation();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [cascadeCount, setCascadeCount] = useState(0);
  const [cascadePreviewCount, setCascadePreviewCount] = useState<number | null>(null);
  const [isCounting, setIsCounting] = useState(false);

  const nodes = useMemo(() => buildDelegationNodes(delegation), [delegation]);
  const scopeSummary = useMemo(
    () => summarizeScopeLabel(delegation.allowedActions),
    [delegation.allowedActions],
  );

  const handleNavigate = (agentId: string) => {
    navigate(`/agents/${encodeURIComponent(agentId)}`);
  };

  const loadCascadePreview = useCallback(async () => {
    if (!loadCascadeCount) {
      setCascadePreviewCount(0);
      return 0;
    }
    if (cascadePreviewCount !== null) {
      return cascadePreviewCount;
    }
    setIsCounting(true);
    try {
      const count = await loadCascadeCount(delegation.jti);
      setCascadePreviewCount(count);
      return count;
    } finally {
      setIsCounting(false);
    }
  }, [cascadePreviewCount, delegation.jti, loadCascadeCount]);

  useEffect(() => {
    void loadCascadePreview();
  }, [loadCascadePreview]);

  const handleOpenDialog = async () => {
    setDialogOpen(true);
    setCascadeCount(await loadCascadePreview());
  };

  const handleConfirm = async () => {
    await revokeDelegation.mutateAsync({ jti: delegation.jti });
    setDialogOpen(false);
  };

  if (delegation.chain.length === 0) {
    return (
      <>
        <div data-testid="delegation-direct-call">
          <InstrumentCard accent="muted" className="p-6 text-center">
            <p className="text-sm font-medium text-foreground">Direct call</p>
            <p className="mt-2 text-sm text-muted-foreground">
              No upstream delegation chain is attached to this token.
            </p>
          </InstrumentCard>
        </div>
        <RevokeDelegationDialog
          open={dialogOpen}
          cascadeCount={cascadeCount}
          isCounting={isCounting}
          isPending={revokeDelegation.isPending}
          onClose={() => setDialogOpen(false)}
          onConfirm={handleConfirm}
        />
      </>
    );
  }

  return (
    <>
      <ul
        role="tree"
        className="space-y-4"
        data-testid="delegation-chain-tree"
        data-motion-mode={shouldAnimate ? "full" : "reduced"}
      >
        {nodes.map((node, index) => {
          const isLeaf = index === nodes.length - 1;
          return (
            <li
              key={`${node.agentId}-${index}`}
              role="treeitem"
              aria-level={index + 1}
              aria-selected={isLeaf}
              className={cn(
                "list-none",
                mobileDepthClass(index),
                "sm:pl-0",
                shouldAnimate && "animate-in fade-in slide-in-from-top-1 duration-300",
              )}
              style={shouldAnimate ? { animationDelay: `${index * 60}ms` } : undefined}
            >
              <DelegationChainNode
                delegation={delegation}
                agentId={node.agentId}
                depth={index}
                status={node.status}
                riskTier={riskTierByAgent?.[node.agentId]}
                expiresLabel={node.expiresLabel}
                isLeaf={isLeaf && node.status !== "revoked"}
                onNavigate={handleNavigate}
                onRequestRevoke={isLeaf ? handleOpenDialog : undefined}
                revokeDescriptionId={isLeaf ? `delegation-revoke-desc-${delegation.jti}` : undefined}
                revokeDescription={
                  isLeaf
                    ? `Revoking cascades to ${cascadePreviewCount ?? 0} downstream delegation${(cascadePreviewCount ?? 0) === 1 ? "" : "s"}.`
                    : undefined
                }
              />

              {!isLeaf && (
                <div
                  className="ml-7 mt-3 hidden items-center gap-3 sm:flex"
                  data-testid={`delegation-connector-${index}`}
                >
                  <div className="flex flex-col items-center">
                    <span className="h-8 w-px bg-border/70" />
                    <ArrowDown className="h-4 w-4 text-muted-foreground" aria-hidden />
                  </div>
                  <div className="flex flex-wrap items-center gap-2 rounded-full border border-border/50 bg-surface-2/40 px-3 py-1.5">
                    <KeyRound className="h-3.5 w-3.5 text-cordum" aria-hidden />
                    {scopeSummary.visible.length > 0 ? (
                      scopeSummary.visible.map((action) => (
                        <StatusBadge
                          key={`${index}-${action}`}
                          variant="muted"
                          className="text-[11px]"
                        >
                          {action}
                        </StatusBadge>
                      ))
                    ) : (
                      <StatusBadge variant="muted" className="text-[11px]">
                        all actions
                      </StatusBadge>
                    )}
                    {scopeSummary.overflow > 0 && (
                      <StatusBadge variant="info" className="text-[11px]">
                        +{scopeSummary.overflow} more
                      </StatusBadge>
                    )}
                    <ArrowRight className="h-3.5 w-3.5 text-muted-foreground" aria-hidden />
                  </div>
                </div>
              )}
            </li>
          );
        })}
      </ul>

      <RevokeDelegationDialog
        open={dialogOpen}
        cascadeCount={cascadeCount}
        isCounting={isCounting}
        isPending={revokeDelegation.isPending}
        onClose={() => setDialogOpen(false)}
        onConfirm={handleConfirm}
      />
    </>
  );
}

export function buildDelegationNodes(delegation: DelegationView): ChainNode[] {
  const now = Date.now();
  const chainAgents = delegation.chain.map((link) => link.agentId).filter(Boolean);
  const orderedAgents = [...chainAgents, delegation.audience].filter(Boolean);
  return orderedAgents.map((agentId, index) => {
    const isLeaf = index === orderedAgents.length - 1;
    const status = isLeaf ? getDelegationNodeStatus(delegation, now) : "active";
    return {
      agentId,
      status,
      expiresLabel: formatDelegationExpiry(delegation.expiresAt, status, now),
    };
  });
}

export function getDelegationNodeStatus(
  delegation: DelegationView,
  now = Date.now(),
): DelegationNodeStatus {
  if (delegation.revoked) return "revoked";
  const expiresAt = Date.parse(delegation.expiresAt);
  if (Number.isFinite(expiresAt) && expiresAt <= now) return "expired";
  return "active";
}

export function formatDelegationExpiry(
  expiresAt: string,
  status: DelegationNodeStatus,
  now = Date.now(),
): string {
  if (!expiresAt) return "No expiry recorded";
  if (status === "revoked") return "Revoked";
  const timestamp = Date.parse(expiresAt);
  if (!Number.isFinite(timestamp)) return "Expiry unavailable";
  if (timestamp <= now) {
    return `Expired ${formatRelativeTime(new Date(timestamp).toISOString())}`;
  }
  const diff = timestamp - now;
  if (diff < 60_000) return "Expires in <1m";
  if (diff < 3_600_000) return `Expires in ${Math.floor(diff / 60_000)}m`;
  if (diff < 86_400_000) return `Expires in ${Math.floor(diff / 3_600_000)}h`;
  return `Expires on ${new Date(timestamp).toLocaleDateString()}`;
}

export function countCascadeDescendants(
  delegations: DelegationView[],
  targetJti: string,
): number {
  return delegations.filter(
    (delegation) =>
      delegation.jti !== targetJti &&
      delegation.chain.some(
        (link) => link.jti === targetJti || link.parentJti === targetJti,
      ),
  ).length;
}

function mobileDepthClass(depth: number): string {
  if (depth >= 3) return "pl-9";
  if (depth === 2) return "pl-6";
  if (depth === 1) return "pl-3";
  return "pl-0";
}
