import { useMemo, useState } from "react";
import { motion } from "framer-motion";
import { Search, ShieldAlert, X } from "lucide-react";
import type { DelegationStatus, DelegationView } from "@/api/types";
import { PageHeader } from "@/components/layout/PageHeader";
import { RevokeDelegationDialog } from "@/components/delegations/RevokeDelegationDialog";
import { summarizeScopeLabel } from "@/components/delegations/DelegationChainNode";
import {
  countCascadeDescendants,
  DelegationChainViz,
  formatDelegationExpiry,
  getDelegationNodeStatus,
} from "@/components/delegations/DelegationChainViz";
import { Button } from "@/components/ui/Button";
import { Checkbox } from "@/components/ui/Checkbox";
import { DataTable } from "@/components/ui/DataTable";
import { DetailList } from "@/components/ui/DetailList";
import { Drawer } from "@/components/ui/Drawer";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Input } from "@/components/ui/Input";
import { Select } from "@/components/ui/Select";
import { SkeletonCard, SkeletonTable } from "@/components/ui/Skeleton";
import { StatTile } from "@/components/ui/StatTile";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Tabs } from "@/components/ui/Tabs";
import { useAllDelegations, useRevokeDelegation } from "@/hooks/useDelegations";

type DelegationFilterTab = DelegationStatus;

export default function DelegationsPage() {
  const [activeTab, setActiveTab] = useState<DelegationFilterTab>("all");
  const [search, setSearch] = useState("");
  const [scopeFilter, setScopeFilter] = useState("");
  const [expiringSoonOnly, setExpiringSoonOnly] = useState(false);
  const [selectedJti, setSelectedJti] = useState<string | null>(null);
  const [pendingRowRevokeJti, setPendingRowRevokeJti] = useState<string | null>(null);
  const delegationsQuery = useAllDelegations({ limit: 200 });
  const revokeDelegation = useRevokeDelegation();

  const delegations = useMemo(
    () => flattenDelegations(delegationsQuery.data?.pages),
    [delegationsQuery.data?.pages],
  );

  const counts = useMemo(() => {
    let active = 0;
    let revoked = 0;
    let expired = 0;

    for (const delegation of delegations) {
      const status = getDelegationNodeStatus(delegation);
      if (status === "active") active += 1;
      if (status === "revoked") revoked += 1;
      if (status === "expired") expired += 1;
    }

    return {
      all: delegations.length,
      active,
      revoked,
      expired,
    };
  }, [delegations]);

  const filteredDelegations = useMemo(() => {
    const normalizedSearch = search.trim().toLowerCase();
    return delegations.filter((delegation) => {
      const status = getDelegationNodeStatus(delegation);
      if (activeTab !== "all" && status !== activeTab) {
        return false;
      }
      if (scopeFilter && !delegation.allowedActions.includes(scopeFilter)) {
        return false;
      }
      if (expiringSoonOnly && !isExpiringSoon(delegation.expiresAt, status)) {
        return false;
      }
      if (!normalizedSearch) {
        return true;
      }
      const haystack = [
        delegation.jti,
        delegation.issuer,
        delegation.subject,
        delegation.audience,
        ...delegation.allowedActions,
        ...delegation.allowedTopics,
      ]
        .join(" ")
        .toLowerCase();
      return haystack.includes(normalizedSearch);
    });
  }, [activeTab, delegations, expiringSoonOnly, scopeFilter, search]);

  const selectedDelegation = useMemo(
    () =>
      selectedJti
        ? delegations.find((delegation) => delegation.jti === selectedJti) ?? null
        : null,
    [delegations, selectedJti],
  );
  const rowRevokeDelegation = useMemo(
    () =>
      pendingRowRevokeJti
        ? delegations.find((delegation) => delegation.jti === pendingRowRevokeJti) ?? null
        : null,
    [delegations, pendingRowRevokeJti],
  );
  const scopeOptions = useMemo(
    () =>
      Array.from(
        new Set(
          delegations.flatMap((delegation) => delegation.allowedActions.filter(Boolean)),
        ),
      )
        .sort((left, right) => left.localeCompare(right))
        .map((value) => ({ value, label: value })),
    [delegations],
  );

  const tabs = [
    { id: "all", label: "All", count: counts.all },
    { id: "active", label: "Active", count: counts.active },
    { id: "revoked", label: "Revoked", count: counts.revoked },
    { id: "expired", label: "Expired", count: counts.expired },
  ];

  if (delegationsQuery.isLoading) {
    return (
      <div className="space-y-6">
        <PageHeader
          label="Govern"
          title="Delegations"
          subtitle="Inspect active delegation chains and revoke downstream access."
        />
        <div className="grid gap-4 md:grid-cols-3">
          <SkeletonCard />
          <SkeletonCard />
          <SkeletonCard />
        </div>
        <SkeletonCard />
        <SkeletonTable rows={6} />
      </div>
    );
  }

  if (delegationsQuery.isError) {
    return (
      <div className="space-y-6">
        <PageHeader
          label="Govern"
          title="Delegations"
          subtitle="Inspect active delegation chains and revoke downstream access."
        />
        <ErrorBanner
          title="Failed to load delegations"
          message={
            delegationsQuery.error?.message ??
            "An unexpected error occurred while loading delegation data."
          }
          onRetry={() => {
            void delegationsQuery.refetch();
          }}
        />
      </div>
    );
  }

  return (
    <motion.div
      className="space-y-6"
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3 }}
    >
      <PageHeader
        label="Govern"
        title="Delegations"
        subtitle="Inspect active delegation chains and revoke downstream access."
        actions={
          <Button variant="outline" size="sm" onClick={() => void delegationsQuery.refetch()}>
            Refresh
          </Button>
        }
      />

      {delegations.length === 0 ? (
        <EmptyState
          icon={<ShieldAlert className="h-5 w-5" />}
          title="No delegations issued"
          description="Scoped delegation tokens will appear here once agents mint downstream access."
        />
      ) : (
        <>
          <div className="grid gap-4 md:grid-cols-3">
            <StatTile
              label="Active"
              value={counts.active}
              accent="healthy"
              helperText="Currently valid delegation chains."
            />
            <StatTile
              label="Revoked"
              value={counts.revoked}
              accent="danger"
              helperText="Tokens revoked across the fleet."
            />
            <StatTile
              label="Expired"
              value={counts.expired}
              accent="warning"
              helperText="Delegations that are no longer valid."
            />
          </div>

          <div className="instrument-card space-y-4">
            <div className="flex flex-col gap-3">
              <Tabs
                tabs={tabs}
                activeTab={activeTab}
                onChange={(value) => setActiveTab(value as DelegationFilterTab)}
                ariaLabel="Delegation status filters"
              />
              <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_220px_auto] lg:items-center">
                <Input
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  placeholder="Search by agent, topic, action, or token..."
                  icon={<Search className="h-4 w-4" />}
                />
                <Select
                  value={scopeFilter}
                  onChange={(event) => setScopeFilter(event.target.value)}
                  options={[
                    { value: "", label: "All scopes" },
                    ...scopeOptions,
                  ]}
                  aria-label="Filter by delegation scope"
                />
                <Checkbox
                  checked={expiringSoonOnly}
                  onChange={(event) => setExpiringSoonOnly(event.target.checked)}
                  label="Expiring in 24h"
                  wrapperClassName="justify-start"
                />
              </div>
            </div>

            <DataTable
              data={filteredDelegations}
              keyExtractor={(row) => row.jti}
              onRowClick={(row) => setSelectedJti(row.jti)}
              emptyMessage="No delegations match the current filters"
              columns={[
                {
                  key: "issuer",
                  header: "Issuer",
                  render: (row) => (
                    <div className="text-sm font-medium text-foreground">{row.issuer}</div>
                  ),
                },
                {
                  key: "subject",
                  header: "Subject",
                  render: (row) => (
                    <div className="text-sm text-foreground">{row.subject}</div>
                  ),
                },
                {
                  key: "audience",
                  header: "Audience",
                  render: (row) => (
                    <div className="text-sm text-foreground">{row.audience}</div>
                  ),
                },
                {
                  key: "scope",
                  header: "Scope",
                  render: (row) => {
                    const summary = summarizeScopeLabel(row.allowedActions);
                    return (
                      <div className="space-y-1">
                        <div className="text-sm text-foreground">
                          {summary.visible.length > 0
                            ? summary.visible.join(", ")
                            : "all actions"}
                          {summary.overflow > 0 ? ` +${summary.overflow}` : ""}
                        </div>
                        <div className="text-xs text-muted-foreground">
                          {row.allowedTopics.length > 0
                            ? `${row.allowedTopics.length} topic${row.allowedTopics.length === 1 ? "" : "s"}`
                            : "All tenant topics"}
                        </div>
                      </div>
                    );
                  },
                },
                {
                  key: "depth",
                  header: "Chain Depth",
                  render: (row) => (
                    <div className="text-sm text-foreground">{row.chainDepth}</div>
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
                {
                  key: "status",
                  header: "Status",
                  render: (row) => {
                    const status = getDelegationNodeStatus(row);
                    return (
                      <StatusBadge variant={statusVariant(status)}>{status}</StatusBadge>
                    );
                  },
                },
                {
                  key: "actions",
                  header: "Actions",
                  align: "right",
                  render: (row) => {
                    const status = getDelegationNodeStatus(row);
                    return (
                      <div className="flex items-center justify-end gap-2">
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={(event) => {
                            event.stopPropagation();
                            setSelectedJti(row.jti);
                          }}
                        >
                          Open
                        </Button>
                        {status === "active" ? (
                          <Button
                            variant="danger"
                            size="sm"
                            onClick={(event) => {
                              event.stopPropagation();
                              setPendingRowRevokeJti(row.jti);
                            }}
                          >
                            Revoke
                          </Button>
                        ) : null}
                      </div>
                    );
                  },
                },
              ]}
            />
          </div>
        </>
      )}

      <Drawer
        open={Boolean(selectedDelegation)}
        onClose={() => setSelectedJti(null)}
        size="xl"
        label="Delegation details"
      >
        {selectedDelegation ? (
          <div className="space-y-6">
            <div className="flex items-start justify-between gap-4">
              <div>
                <p className="text-xs font-semibold uppercase tracking-[0.18em] text-muted-foreground">
                  Delegation chain
                </p>
                <h2 className="mt-2 text-xl font-semibold text-foreground">
                  {selectedDelegation.issuer} → {selectedDelegation.audience}
                </h2>
                <p className="mt-1 text-sm text-muted-foreground">
                  Subject {selectedDelegation.subject}
                </p>
              </div>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setSelectedJti(null)}
                aria-label="Close delegation details"
              >
                <X className="h-4 w-4" />
              </Button>
            </div>

            <DetailList
              items={[
                {
                  label: "Token ID",
                  value: selectedDelegation.jti,
                  mono: true,
                  align: "left",
                },
                {
                  label: "Issued",
                  value: selectedDelegation.issuedAt || "—",
                  mono: true,
                  align: "left",
                },
                {
                  label: "Expires",
                  value: selectedDelegation.expiresAt || "—",
                  mono: true,
                  align: "left",
                },
                {
                  label: "Status",
                  value: getDelegationNodeStatus(selectedDelegation),
                  align: "left",
                },
                {
                  label: "Chain depth",
                  value: selectedDelegation.chainDepth,
                  align: "left",
                },
                {
                  label: "Actions",
                  value:
                    selectedDelegation.allowedActions.length > 0
                      ? selectedDelegation.allowedActions.join(", ")
                      : "all actions",
                  align: "left",
                },
                {
                  label: "Topics",
                  value:
                    selectedDelegation.allowedTopics.length > 0
                      ? selectedDelegation.allowedTopics.join(", ")
                      : "All tenant topics",
                  align: "left",
                },
              ]}
            />

            <DelegationChainViz
              delegation={selectedDelegation}
              loadCascadeCount={async (jti) => countCascadeDescendants(delegations, jti)}
            />
          </div>
        ) : null}
      </Drawer>

      <RevokeDelegationDialog
        open={Boolean(rowRevokeDelegation)}
        cascadeCount={
          rowRevokeDelegation
            ? countCascadeDescendants(delegations, rowRevokeDelegation.jti)
            : 0
        }
        isPending={revokeDelegation.isPending}
        onClose={() => setPendingRowRevokeJti(null)}
        onConfirm={() => {
          if (!rowRevokeDelegation) return;
          void revokeDelegation
            .mutateAsync({ jti: rowRevokeDelegation.jti })
            .then(() => setPendingRowRevokeJti(null));
        }}
      />
    </motion.div>
  );
}

function flattenDelegations(
  pages: ReadonlyArray<{ items: DelegationView[] }> | undefined,
): DelegationView[] {
  return (pages ?? []).flatMap((page) => page.items).sort((left, right) => {
    const leftIssuedAt = Date.parse(left.issuedAt);
    const rightIssuedAt = Date.parse(right.issuedAt);
    return (Number.isFinite(rightIssuedAt) ? rightIssuedAt : 0)
      - (Number.isFinite(leftIssuedAt) ? leftIssuedAt : 0);
  });
}

function statusVariant(status: DelegationStatus): "healthy" | "danger" | "warning" {
  switch (status) {
    case "revoked":
      return "danger";
    case "expired":
      return "warning";
    default:
      return "healthy";
  }
}

function isExpiringSoon(
  expiresAt: string,
  status: Exclude<DelegationStatus, "all">,
  now = Date.now(),
): boolean {
  if (status !== "active") return false;
  const expiresAtMs = Date.parse(expiresAt);
  if (!Number.isFinite(expiresAtMs)) return false;
  return expiresAtMs > now && expiresAtMs - now <= 86_400_000;
}
