import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { AlertTriangle, FileText, KeyRound, Search } from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { Card, CardTitle } from "@/components/ui/Card";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { Button } from "@/components/ui/Button";
import { ChainIntegrityWidget } from "@/components/ChainIntegrityWidget";
import { SignatureBadge } from "@/components/SignatureBadge";
import { usePolicyBundles, encodePolicyBundleId } from "@/hooks/usePolicies";
import { useConfigStore } from "@/state/config";
import { cn } from "@/lib/utils";
import type { PolicyBundle } from "@/api/types";

// ---------------------------------------------------------------------------
// GovernanceVerificationPage
//
// A dedicated compliance-surface page. Two stacked cards:
//   1. <ChainIntegrityWidget /> — current tenant's audit chain status,
//      gap drill-down, re-verify.
//   2. Signed policy bundles inventory — per-bundle SignatureBadge with
//      key_id / sha256 / signed-bytes columns, sortable by name or
//      signed state, with a summary header (N/M bundles signed).
//
// This page is mentioned in the Govern nav behind a requiresAdmin gate.
// The UI degrades gracefully for non-admins (widget becomes read-only,
// re-verify hidden). Individual bundle rows link to the BundleDetailPage.
// ---------------------------------------------------------------------------

type SortField = "name" | "signed";
type SortDir = "asc" | "desc";

interface BundleSortKey {
  field: SortField;
  dir: SortDir;
}

function classifySignedState(b: PolicyBundle): 0 | 1 | 2 {
  // Sort order: signed (0) → unsigned (1) → unknown (2). We want
  // signed-first in asc order so compliance reviewers can quickly see
  // which bundles still need signing by scrolling to the bottom.
  if (b.signed === true) return 0;
  if (b.signed === false) return 1;
  return 2;
}

export function sortBundles(
  bundles: PolicyBundle[],
  sort: BundleSortKey,
): PolicyBundle[] {
  const copy = [...bundles];
  copy.sort((a, b) => {
    if (sort.field === "name") {
      const an = (a.name || a.id).toLowerCase();
      const bn = (b.name || b.id).toLowerCase();
      const cmp = an.localeCompare(bn);
      return sort.dir === "asc" ? cmp : -cmp;
    }
    const ac = classifySignedState(a);
    const bc = classifySignedState(b);
    if (ac !== bc) return sort.dir === "asc" ? ac - bc : bc - ac;
    // Secondary by name for stable ordering
    const an = (a.name || a.id).toLowerCase();
    const bn = (b.name || b.id).toLowerCase();
    return an.localeCompare(bn);
  });
  return copy;
}

export function countSigned(bundles: PolicyBundle[]): {
  signed: number;
  total: number;
  unsigned: number;
  unknown: number;
} {
  let signed = 0;
  let unsigned = 0;
  let unknown = 0;
  for (const b of bundles) {
    if (b.signed === true) signed += 1;
    else if (b.signed === false) unsigned += 1;
    else unknown += 1;
  }
  return { signed, total: bundles.length, unsigned, unknown };
}

function truncate(s: string | undefined, max: number): string {
  if (!s) return "—";
  if (s.length <= max) return s;
  return `${s.slice(0, max)}…`;
}

export default function GovernanceVerificationPage() {
  const navigate = useNavigate();
  const tenantId = useConfigStore((s) => s.tenantId);

  const { data, isLoading, isError, error, refetch } = usePolicyBundles();
  const bundles = useMemo(() => data?.items ?? [], [data]);

  const [sort, setSort] = useState<BundleSortKey>({
    field: "signed",
    dir: "asc",
  });
  const [filter, setFilter] = useState<string>("");

  const counts = useMemo(() => countSigned(bundles), [bundles]);
  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return bundles;
    return bundles.filter((b) =>
      (b.name || b.id).toLowerCase().includes(q),
    );
  }, [bundles, filter]);
  const sorted = useMemo(() => sortBundles(filtered, sort), [filtered, sort]);

  const toggleSort = (field: SortField) => {
    setSort((prev) =>
      prev.field === field
        ? { field, dir: prev.dir === "asc" ? "desc" : "asc" }
        : { field, dir: "asc" },
    );
  };

  const signedPercent =
    counts.total > 0 ? Math.round((counts.signed / counts.total) * 100) : 0;

  return (
    <div className="space-y-6">
      <PageHeader
        label="Govern"
        title="Verification"
        subtitle="Signature status for every policy bundle and live audit chain integrity. The dashboard view for SOC2 evidence and incident response."
      />

      {/* Chain integrity widget */}
      <ChainIntegrityWidget tenant={tenantId} />

      {/* Signed policy bundles */}
      <Card className="relative overflow-hidden p-0">
        <span
          aria-hidden="true"
          className="pointer-events-none absolute inset-x-0 top-0 h-1 bg-gradient-to-r from-success/70 via-success/30 to-transparent"
        />
        <div className="flex flex-col gap-4 border-b border-border/60 p-6 md:flex-row md:items-center md:justify-between">
          <div>
            <div className="flex items-center gap-2">
              <CardTitle className="text-base">Signed Policy Bundles</CardTitle>
              {!isLoading && !isError && (
                <span
                  className={cn(
                    "inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-[11px] font-semibold tracking-[0.06em]",
                    counts.total === 0
                      ? "bg-muted text-muted-foreground border-border"
                      : counts.signed === counts.total
                        ? "bg-success/10 text-success border-success/25"
                        : counts.signed === 0
                          ? "bg-warning/10 text-warning border-warning/25"
                          : "bg-accent/10 text-[var(--color-accent)] border-accent/25",
                  )}
                  aria-label={`${counts.signed} of ${counts.total} bundles signed`}
                >
                  {counts.signed} / {counts.total} signed
                  <span className="font-mono opacity-70">· {signedPercent}%</span>
                </span>
              )}
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              Ed25519 signatures are verified by the safety kernel at load
              time. Strict mode rejects any unsigned bundle outright.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <div className="relative">
              <Search
                className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
                aria-hidden="true"
              />
              <input
                type="search"
                placeholder="Filter bundles…"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                className={cn(
                  "h-8 w-52 rounded-full border border-border bg-background pl-8 pr-3 text-xs",
                  "placeholder:text-muted-foreground/70",
                  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cordum/40",
                )}
                aria-label="Filter bundles by name"
              />
            </div>
          </div>
        </div>

        {isLoading && (
          <div className="grid gap-3 p-6 md:grid-cols-2">
            <SkeletonCard />
            <SkeletonCard />
            <SkeletonCard />
            <SkeletonCard />
          </div>
        )}

        {isError && (
          <EmptyState
            icon={<AlertTriangle className="h-5 w-5" />}
            title="Unable to load policy bundles"
            description={
              error instanceof Error
                ? error.message
                : "Unexpected error loading bundle inventory."
            }
            action={
              <Button variant="outline" size="sm" onClick={() => void refetch()}>
                Retry
              </Button>
            }
          />
        )}

        {!isLoading && !isError && bundles.length === 0 && (
          <EmptyState
            icon={<FileText className="h-5 w-5" />}
            title="No policy bundles"
            description="Create a bundle in Policy Studio to see its signature status here."
          />
        )}

        {!isLoading && !isError && bundles.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border/60 text-left text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
                  <th className="sticky left-0 bg-card px-6 py-3 font-semibold">
                    <button
                      type="button"
                      onClick={() => toggleSort("name")}
                      className="inline-flex items-center gap-1 hover:text-ink"
                      aria-label={
                        sort.field === "name"
                          ? `Sort by name, currently ${sort.dir === "asc" ? "ascending" : "descending"}`
                          : "Sort by name"
                      }
                    >
                      Bundle
                      <SortIndicator active={sort.field === "name"} dir={sort.dir} />
                    </button>
                  </th>
                  <th className="px-3 py-3 font-semibold">
                    <button
                      type="button"
                      onClick={() => toggleSort("signed")}
                      className="inline-flex items-center gap-1 hover:text-ink"
                      aria-label={
                        sort.field === "signed"
                          ? `Sort by signature, currently ${sort.dir === "asc" ? "ascending" : "descending"}`
                          : "Sort by signature"
                      }
                    >
                      Signature
                      <SortIndicator active={sort.field === "signed"} dir={sort.dir} />
                    </button>
                  </th>
                  <th className="px-3 py-3 font-semibold">Key ID</th>
                  <th className="px-3 py-3 font-semibold">SHA-256</th>
                  <th className="px-6 py-3 font-semibold text-right">Updated</th>
                </tr>
              </thead>
              <tbody>
                {sorted.map((b) => (
                  <BundleRow
                    key={b.id}
                    bundle={b}
                    onOpen={() =>
                      navigate(
                        `/govern/bundles/${encodeURIComponent(encodePolicyBundleId(b.id))}`,
                      )
                    }
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {!isLoading && !isError && counts.total > 0 && (
        <p className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <KeyRound className="h-3 w-3" aria-hidden="true" />
          Signatures are stored alongside bundle content and regenerated on
          every save when the signing key is configured.
        </p>
      )}
    </div>
  );
}

function SortIndicator({ active, dir }: { active: boolean; dir: SortDir }) {
  if (!active) {
    return (
      <span aria-hidden="true" className="font-mono text-[10px] opacity-40">
        ↕
      </span>
    );
  }
  return (
    <span aria-hidden="true" className="font-mono text-[10px]">
      {dir === "asc" ? "↑" : "↓"}
    </span>
  );
}

function BundleRow({
  bundle,
  onOpen,
}: {
  bundle: PolicyBundle;
  onOpen: () => void;
}) {
  const sig = bundle.signature;
  return (
    <tr
      className="group cursor-pointer border-b border-border/40 transition-colors hover:bg-muted/40"
      onClick={onOpen}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onOpen();
        }
      }}
      tabIndex={0}
      role="button"
      aria-label={`Open bundle ${bundle.name || bundle.id}`}
      data-bundle-id={bundle.id}
    >
      <td className="sticky left-0 bg-card px-6 py-3 group-hover:bg-muted/40">
        <div className="flex flex-col">
          <span className="font-medium text-ink">{bundle.name || bundle.id}</span>
          <span className="font-mono text-[11px] text-muted-foreground">
            {bundle.id}
          </span>
        </div>
      </td>
      <td className="px-3 py-3">
        <SignatureBadge
          signed={bundle.signed ?? "unknown"}
          publicKeyId={sig?.key_id}
          size="sm"
        />
      </td>
      <td className="px-3 py-3 font-mono text-[11px] text-muted-foreground">
        {truncate(sig?.key_id, 24)}
      </td>
      <td className="px-3 py-3 font-mono text-[11px] text-muted-foreground">
        {truncate(sig?.hash, 16)}
      </td>
      <td className="px-6 py-3 text-right text-xs text-muted-foreground tabular-nums">
        {bundle.updatedAt
          ? new Date(bundle.updatedAt).toLocaleDateString()
          : "—"}
      </td>
    </tr>
  );
}
