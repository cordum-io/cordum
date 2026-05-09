import { GitBranch, Plus, Search } from "lucide-react";
import { useMemo } from "react";
import { Link } from "react-router-dom";
import { useQueryState, parseAsString } from "nuqs";
import type { ColumnDef } from "@tanstack/react-table";
import { PageHeader } from "@/components/layout/PageHeader";
import { EmptyState } from "@/components/ui/EmptyState";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { DataTable } from "@/components/primitives/DataTable";
import { formatRelativeTime } from "@/lib/utils";
import { useBundlesList } from "@/hooks/useBundlesList";
import type { Bundle } from "@/api/generated/model/bundle";

/**
 * Policy Studio — Bundles surface (Dashboard 5 step 4a).
 *
 * Renders the unified Backend-1.5 Bundle list with a scope/search filter
 * row and the canonical DataTable primitive. Filter state is mirrored to
 * the URL via nuqs so deep-links + back/forward navigation work.
 *
 * Status dot column derives from the deployment audit metadata: green if
 * the bundle has any deployed version, grey otherwise. The "+ New bundle"
 * button is currently a no-op CTA — bundle authoring ships in a follow-up
 * once Backend 2's CreateBundle endpoint lands.
 */

interface BundleRow {
  id: string;
  name: string;
  scopeLabel: string;
  versionCount: number;
  lastDeployedAt: string | null;
  deployed: boolean;
}

function scopeBindingLabel(bundle: Bundle): string {
  const scope = bundle.scope_binding;
  if (!scope) return "—";
  if (scope.kind === "global") return "global";
  return scope.value ? `${scope.kind}:${scope.value}` : scope.kind;
}

function pickLastDeployedAt(bundle: Bundle): string | null {
  const versions = bundle.versions ?? [];
  if (versions.length === 0) return null;
  // BundleVersion.deployed_at is the canonical timestamp for "last
  // deployed". We pick the maximum across versions; once Backend 2
  // surfaces a top-level field we'll collapse this to a direct accessor.
  let max: string | null = null;
  for (const v of versions) {
    const ts = v.deployed_at;
    if (ts && (!max || ts > max)) max = ts;
  }
  return max;
}

function toRow(bundle: Bundle): BundleRow {
  const lastDeployedAt = pickLastDeployedAt(bundle);
  return {
    id: bundle.id,
    name: bundle.name,
    scopeLabel: scopeBindingLabel(bundle),
    versionCount: bundle.versions?.length ?? 0,
    lastDeployedAt,
    deployed: lastDeployedAt !== null,
  };
}

const STATUS_DOT_DEPLOYED = "var(--color-success)";
const STATUS_DOT_DRAFT = "var(--color-muted)";

function StatusDot({ deployed }: { deployed: boolean }) {
  return (
    <span
      aria-label={deployed ? "Deployed" : "Draft (never deployed)"}
      className="inline-block h-2 w-2 rounded-full"
      style={{ backgroundColor: deployed ? STATUS_DOT_DEPLOYED : STATUS_DOT_DRAFT }}
    />
  );
}

export default function BundlesPage() {
  const [scope, setScope] = useQueryState(
    "scope",
    parseAsString.withDefault("").withOptions({ clearOnDefault: true }),
  );
  const [search, setSearch] = useQueryState(
    "search",
    parseAsString.withDefault("").withOptions({ clearOnDefault: true }),
  );

  const filters = useMemo(
    () => ({
      scope: scope || undefined,
      search: search || undefined,
    }),
    [scope, search],
  );

  const { data, isPending, isError } = useBundlesList(filters);
  const items = data?.items ?? [];
  const rows = useMemo(() => items.map(toRow), [items]);

  const columns = useMemo<ColumnDef<BundleRow, unknown>[]>(
    () => [
      {
        accessorKey: "name",
        header: "Name",
        cell: ({ row }) => (
          <Link
            to={`/policies/bundles/${row.original.id}`}
            className="font-medium text-foreground hover:text-cordum"
            data-row-action
          >
            {row.original.name}
          </Link>
        ),
      },
      {
        accessorKey: "scopeLabel",
        header: "Active for",
        cell: ({ row }) => (
          <span className="font-mono text-xs text-muted-foreground">
            {row.original.scopeLabel}
          </span>
        ),
      },
      {
        accessorKey: "versionCount",
        header: "Versions",
        cell: ({ row }) => (
          <span className="tabular-nums">{row.original.versionCount}</span>
        ),
      },
      {
        accessorKey: "lastDeployedAt",
        header: "Last deployed",
        cell: ({ row }) =>
          row.original.lastDeployedAt
            ? formatRelativeTime(row.original.lastDeployedAt)
            : <span className="text-muted-foreground">never</span>,
      },
      {
        accessorKey: "deployed",
        header: "Status",
        cell: ({ row }) => <StatusDot deployed={row.original.deployed} />,
      },
    ],
    [],
  );

  const filtersActive = Boolean(scope || search);

  return (
    <div className="space-y-6">
      <PageHeader
        label="Policy Studio"
        title="Policy Bundles"
        subtitle="Group rules + deploy to scopes"
        actions={
          <Button
            variant="primary"
            size="sm"
            // No-op until Backend 2's CreateBundle endpoint ships; the
            // affordance is intentionally visible so the IA reads as
            // complete to a CISO browsing the surface.
            onClick={() => {
              /* placeholder — Dashboard 5 follow-up wires CreateBundle */
            }}
          >
            <Plus className="h-3.5 w-3.5 mr-1" aria-hidden />
            New bundle
          </Button>
        }
      />

      <div className="flex flex-wrap items-center gap-3">
        <div className="flex-1 min-w-[200px]">
          <Input
            type="search"
            placeholder="Search bundles…"
            icon={<Search className="h-3.5 w-3.5" aria-hidden />}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            aria-label="Search bundles by name"
          />
        </div>
        <Input
          type="text"
          placeholder="Scope (e.g. tenant:acme)"
          value={scope}
          onChange={(e) => setScope(e.target.value)}
          aria-label="Filter by scope binding"
          className="max-w-[240px]"
        />
      </div>

      {isError ? (
        <EmptyState
          icon={<GitBranch className="h-5 w-5" />}
          title="Couldn't load bundles"
          description="The bundle list endpoint returned an error. Refresh, or try again once the Bundle Studio backend has settled."
        />
      ) : isPending ? (
        <EmptyState
          icon={<GitBranch className="h-5 w-5" />}
          title="Loading bundles…"
          description="Fetching from /api/v1/policy/bundles."
        />
      ) : rows.length === 0 ? (
        <EmptyState
          icon={<GitBranch className="h-5 w-5" />}
          title={filtersActive ? "No bundles match the active filter" : "No bundles yet"}
          description={
            filtersActive
              ? "Clear the filters to see every bundle in this tenant."
              : "Bundles group rules + bind them to a deployment scope. Create the first one once the Bundle Studio backend ships."
          }
          action={
            filtersActive ? (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  void setScope("");
                  void setSearch("");
                }}
              >
                Clear filters
              </Button>
            ) : (
              <Button variant="primary" size="sm" disabled>
                <Plus className="h-3.5 w-3.5 mr-1" aria-hidden />
                Create your first bundle
              </Button>
            )
          }
        />
      ) : (
        <DataTable<BundleRow>
          columns={columns}
          data={rows}
          emptyState={null}
          initialSorting={[{ id: "name", desc: false }]}
        />
      )}
    </div>
  );
}
