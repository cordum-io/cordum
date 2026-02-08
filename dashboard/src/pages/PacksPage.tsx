import { useState } from "react";
import { Package, Trash2, X, ShoppingBag } from "lucide-react";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Badge } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { Drawer } from "../components/ui/Drawer";
import PackDetail from "../components/packs/PackDetail";
import { MarketplaceBrowser } from "../components/packs/MarketplaceBrowser";
import { usePacks, useUninstallPack } from "../hooks/usePacks";
import { cn } from "../lib/utils";
import type { Pack } from "../api/types";

type PacksTab = "installed" | "marketplace";

function statusVariant(status: string): "success" | "warning" | "danger" | "default" {
  switch (status) {
    case "active":
    case "running":
      return "success";
    case "installing":
    case "updating":
      return "warning";
    case "error":
    case "failed":
      return "danger";
    default:
      return "default";
  }
}

function PackCard({
  pack,
  onClick,
  onUninstall,
}: {
  pack: Pack;
  onClick: (pack: Pack) => void;
  onUninstall: (pack: Pack) => void;
}) {
  return (
    <Card className="flex flex-col justify-between cursor-pointer hover:ring-2 hover:ring-accent/30 transition-shadow" onClick={() => onClick(pack)}>
      <div>
        <CardHeader>
          <div className="flex items-center gap-2">
            <Package className="h-5 w-5 text-accent" />
            <CardTitle>{pack.name}</CardTitle>
          </div>
          <Badge variant={statusVariant(pack.status)}>{pack.status}</Badge>
        </CardHeader>

        <div className="mb-3 flex items-center gap-2 text-sm text-muted">
          <span>v{pack.version}</span>
          {pack.poolAssignment && (
            <>
              <span className="text-border">|</span>
              <span>Pool: {pack.poolAssignment}</span>
            </>
          )}
        </div>

        {pack.capabilities.length > 0 && (
          <div className="mb-4 flex flex-wrap gap-1.5">
            {pack.capabilities.map((cap) => (
              <Badge key={cap} variant="info" className="text-[11px]">
                {cap}
              </Badge>
            ))}
          </div>
        )}
      </div>

      <div className="flex items-center justify-between border-t border-border pt-4 mt-2">
        <span className="text-xs text-muted">
          Jobs 24h: &mdash;
        </span>
        <Button
          variant="ghost"
          size="sm"
          className="text-danger hover:bg-danger/10"
          onClick={(e) => { e.stopPropagation(); onUninstall(pack); }}
        >
          <Trash2 className="h-3.5 w-3.5" />
          Uninstall
        </Button>
      </div>
    </Card>
  );
}

function ConfirmDialog({
  pack,
  isPending,
  onConfirm,
  onCancel,
}: {
  pack: Pack;
  isPending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="surface-card w-full max-w-md rounded-3xl p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h3 className="font-display text-lg font-semibold text-ink">
            Uninstall Pack
          </h3>
          <button
            onClick={onCancel}
            className="rounded-full p-1 hover:bg-surface2"
          >
            <X className="h-4 w-4 text-muted" />
          </button>
        </div>

        <p className="mb-6 text-sm text-muted">
          Are you sure you want to uninstall <strong className="text-ink">{pack.name}</strong> (v{pack.version})?
          This action cannot be undone.
        </p>

        <div className="flex justify-end gap-3">
          <Button variant="ghost" size="sm" onClick={onCancel} disabled={isPending}>
            Cancel
          </Button>
          <Button variant="danger" size="sm" onClick={onConfirm} disabled={isPending}>
            {isPending ? "Uninstalling..." : "Uninstall"}
          </Button>
        </div>
      </div>
    </div>
  );
}

export default function PacksPage() {
  const { data, isLoading, error } = usePacks();
  const uninstall = useUninstallPack();
  const [confirmPack, setConfirmPack] = useState<Pack | null>(null);
  const [selectedPack, setSelectedPack] = useState<Pack | null>(null);
  const [activeTab, setActiveTab] = useState<PacksTab>("installed");

  const packs = data?.items ?? [];

  function handleUninstall() {
    if (!confirmPack) return;
    uninstall.mutate(confirmPack.id, {
      onSuccess: () => setConfirmPack(null),
    });
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="font-display text-2xl font-bold text-ink">Packs</h1>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 rounded-full border border-border p-1 w-fit">
        <button
          type="button"
          className={cn(
            "flex items-center gap-2 rounded-full px-5 py-2 text-xs font-semibold uppercase tracking-widest transition",
            activeTab === "installed"
              ? "bg-accent/15 text-accent"
              : "text-muted hover:text-ink",
          )}
          onClick={() => setActiveTab("installed")}
        >
          <Package className="h-3.5 w-3.5" />
          Installed
        </button>
        <button
          type="button"
          className={cn(
            "flex items-center gap-2 rounded-full px-5 py-2 text-xs font-semibold uppercase tracking-widest transition",
            activeTab === "marketplace"
              ? "bg-accent/15 text-accent"
              : "text-muted hover:text-ink",
          )}
          onClick={() => setActiveTab("marketplace")}
        >
          <ShoppingBag className="h-3.5 w-3.5" />
          Marketplace
        </button>
      </div>

      {activeTab === "installed" && (
        <>
          {isLoading && (
            <p className="text-sm text-muted">Loading packs...</p>
          )}

          {error && (
            <p className="text-sm text-danger">
              Failed to load packs: {error instanceof Error ? error.message : "Unknown error"}
            </p>
          )}

          {!isLoading && !error && packs.length === 0 && (
            <div className="flex flex-col items-center justify-center rounded-3xl border border-dashed border-border py-16 text-center">
              <Package className="mb-3 h-10 w-10 text-muted" />
              <p className="text-sm text-muted">
                No packs installed &mdash; browse the marketplace to get started.
              </p>
            </div>
          )}

          {packs.length > 0 && (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
              {packs.map((pack) => (
                <PackCard key={pack.id} pack={pack} onClick={setSelectedPack} onUninstall={setConfirmPack} />
              ))}
            </div>
          )}
        </>
      )}

      {activeTab === "marketplace" && <MarketplaceBrowser />}

      {confirmPack && (
        <ConfirmDialog
          pack={confirmPack}
          isPending={uninstall.isPending}
          onConfirm={handleUninstall}
          onCancel={() => setConfirmPack(null)}
        />
      )}

      <Drawer open={!!selectedPack} onClose={() => setSelectedPack(null)} size="lg">
        {selectedPack && (
          <PackDetail
            packId={selectedPack.id}
            pack={selectedPack}
            onClose={() => setSelectedPack(null)}
          />
        )}
      </Drawer>
    </div>
  );
}
