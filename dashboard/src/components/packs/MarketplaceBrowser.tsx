import { useMemo, useState } from "react";
import { Download, Loader, ShoppingBag } from "lucide-react";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Select } from "../ui/Select";
import { useMarketplacePacks, useInstallPack, usePacks } from "../../hooks/usePacks";
import type { MarketplacePack, Pack } from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

interface GroupedPack {
  id: string;
  name: string;
  versions: string[];
  latest: MarketplacePack;
  allByVersion: Map<string, MarketplacePack>;
}

function groupByName(packs: MarketplacePack[]): GroupedPack[] {
  const map = new Map<string, MarketplacePack[]>();
  for (const p of packs) {
    const list = map.get(p.id) ?? [];
    list.push(p);
    map.set(p.id, list);
  }
  return [...map.entries()]
    .map(([id, items]) => {
      const sorted = [...items].sort((a, b) => b.version.localeCompare(a.version));
      const byVersion = new Map(sorted.map((p) => [p.version, p]));
      const title = sorted[0]?.title || sorted[0]?.id || id;
      return {
        id,
        name: title,
        versions: sorted.map((p) => p.version),
        latest: sorted[0],
        allByVersion: byVersion,
      };
    })
    .sort((a, b) => a.name.localeCompare(b.name));
}

// ---------------------------------------------------------------------------
// Marketplace card
// ---------------------------------------------------------------------------

function MarketplaceCard({
  group,
  isInstalled,
  onInstall,
  installing,
}: {
  group: GroupedPack;
  isInstalled: boolean;
  onInstall: (pack: MarketplacePack, version: string) => void;
  installing: boolean;
}) {
  const [selectedVersion, setSelectedVersion] = useState(group.versions[0]);
  const pack = group.allByVersion.get(selectedVersion) ?? group.latest;
  const description = pack.description ?? "";
  const author = pack.author;

  return (
    <Card className="flex flex-col justify-between">
      <div>
        <CardHeader>
          <div className="flex items-center gap-2">
            <ShoppingBag className="h-5 w-5 text-accent" />
            <CardTitle>{group.name}</CardTitle>
          </div>
        </CardHeader>

        {description && (
          <p className="mb-3 line-clamp-2 text-sm text-muted">{description}</p>
        )}

        {author && (
          <p className="mb-3 text-xs text-muted">by {author}</p>
        )}

        {(pack.capabilities ?? []).length > 0 && (
          <div className="mb-4 flex flex-wrap gap-1.5">
            {(pack.capabilities ?? []).map((cap) => (
              <Badge key={cap} variant="info" className="text-[11px]">
                {cap}
              </Badge>
            ))}
          </div>
        )}
      </div>

      <div className="flex items-center justify-between border-t border-border pt-4 mt-2">
        {group.versions.length > 1 ? (
          <Select
            value={selectedVersion}
            onChange={(e) => setSelectedVersion(e.target.value)}
            className="w-28 text-xs"
          >
            {group.versions.map((v) => (
              <option key={v} value={v}>
                v{v}
              </option>
            ))}
          </Select>
        ) : (
          <span className="text-xs text-muted">v{selectedVersion}</span>
        )}

        {isInstalled ? (
          <Badge variant="success">Installed</Badge>
        ) : (
          <Button
            size="sm"
            type="button"
            disabled={installing || !pack.catalogId}
            onClick={() => onInstall(pack, selectedVersion)}
          >
            {installing ? (
              <Loader className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <>
                <Download className="h-3.5 w-3.5" />
                Install
              </>
            )}
          </Button>
        )}
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// MarketplaceBrowser
// ---------------------------------------------------------------------------

export function MarketplaceBrowser() {
  const { data: marketplaceData, isLoading, error } = useMarketplacePacks();
  const { data: installedData } = usePacks();
  const installMutation = useInstallPack();

  const marketplacePacks = marketplaceData?.items ?? [];
  const installedPacks = installedData?.items ?? [];

  const installedNames = useMemo(
    () => new Set(installedPacks.map((p) => p.id)),
    [installedPacks],
  );

  const groups = useMemo(() => groupByName(marketplacePacks), [marketplacePacks]);

  const [installingName, setInstallingName] = useState<string | null>(null);

  function handleInstall(pack: MarketplacePack, version: string) {
    setInstallingName(pack.id);
    installMutation.mutate(
      { catalogId: pack.catalogId || "", packId: pack.id, version },
      { onSettled: () => setInstallingName(null) },
    );
  }

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-16 text-sm text-muted">
        <Loader className="mr-2 h-4 w-4 animate-spin" />
        Loading marketplace...
      </div>
    );
  }

  if (error) {
    return (
      <p className="text-sm text-danger">
        Failed to load marketplace:{" "}
        {error instanceof Error ? error.message : "Unknown error"}
      </p>
    );
  }

  if (groups.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center rounded-3xl border border-dashed border-border py-16 text-center">
        <ShoppingBag className="mb-3 h-10 w-10 text-muted" />
        <p className="text-sm text-muted">No packs available in the marketplace.</p>
      </div>
    );
  }

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      {groups.map((group) => (
        <MarketplaceCard
          key={group.id}
          group={group}
          isInstalled={installedNames.has(group.id)}
          onInstall={handleInstall}
          installing={installingName === group.id}
        />
      ))}
    </div>
  );
}
