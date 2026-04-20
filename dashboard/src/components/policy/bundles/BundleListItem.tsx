import { ArrowRight, Clock, Hash, FileText, CopyCheck } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { SignatureBadge } from "@/components/SignatureBadge";
import type { PolicyBundle } from "@/api/types";

function formatUpdatedAt(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  if (ms < 0) return "just now";
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

function getBundleStatusVariant(status?: string): "healthy" | "warning" | "muted" {
  const normalized = (status ?? "").toLowerCase();
  if (normalized === "published") return "healthy";
  if (normalized === "draft") return "warning";
  return "muted";
}

interface BundleListItemProps {
  bundle: PolicyBundle;
  canPublish: boolean;
  onOpen: (bundleId: string) => void;
}

export function BundleListItem({ bundle, canPublish, onOpen }: BundleListItemProps) {
  const ruleCount = bundle.rule_count ?? bundle.rules?.length ?? 0;
  const updatedLabel = bundle.updatedAt
    ? formatUpdatedAt(bundle.updatedAt)
    : undefined;

  return (
    <article className="instrument-card p-4">
      <div className="mb-3 flex items-center justify-between gap-3">
        <div className="min-w-0">
          <p className="text-sm font-semibold text-foreground truncate">
            {bundle.name || bundle.id}
          </p>
          <p className="text-xs text-muted-foreground font-mono truncate">
            {bundle.id}
          </p>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <SignatureBadge
            signed={bundle.signed ?? "unknown"}
            publicKeyId={bundle.signature?.key_id}
            size="sm"
            data-testid="bundle-signature-badge"
          />
          <StatusBadge variant={getBundleStatusVariant(bundle.status)}>
            {bundle.status ?? "unknown"}
          </StatusBadge>
          {bundle.enabled === false && (
            <StatusBadge variant="muted">disabled</StatusBadge>
          )}
          {bundle.shadow ? (
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                onOpen(bundle.id);
              }}
              className="inline-flex items-center gap-1 rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[10px] font-mono uppercase tracking-wider text-amber-400 transition-colors hover:bg-amber-500/20"
              aria-label="Shadow policy active — open shadow tab"
              data-testid="bundle-shadow-badge"
            >
              <CopyCheck className="h-3 w-3" />
              Shadow
            </button>
          ) : (
            <span
              className="inline-flex items-center gap-1 rounded-full border border-border bg-muted/40 px-2 py-0.5 text-[10px] font-mono uppercase tracking-wider text-muted-foreground"
              data-testid="bundle-no-shadow-badge"
            >
              No shadow
            </span>
          )}
        </div>
      </div>

      <dl className="grid grid-cols-2 gap-2 text-xs sm:grid-cols-4">
        <div className="rounded border border-border/70 bg-surface-1 px-2 py-1.5">
          <dt className="flex items-center gap-1 text-muted-foreground">
            <FileText className="h-3 w-3" />
            rules
          </dt>
          <dd className="mt-1 font-mono text-foreground">{ruleCount}</dd>
        </div>
        <div className="rounded border border-border/70 bg-surface-1 px-2 py-1.5">
          <dt className="flex items-center gap-1 text-muted-foreground">
            <Hash className="h-3 w-3" />
            version
          </dt>
          <dd className="mt-1 font-mono text-foreground">
            {bundle.version ?? "—"}
          </dd>
        </div>
        <div className="rounded border border-border/70 bg-surface-1 px-2 py-1.5">
          <dt className="flex items-center gap-1 text-muted-foreground">
            source
          </dt>
          <dd className="mt-1 font-mono text-foreground truncate">
            {bundle.source ?? "—"}
          </dd>
        </div>
        <div className="rounded border border-border/70 bg-surface-1 px-2 py-1.5">
          <dt className="flex items-center gap-1 text-muted-foreground">
            <Clock className="h-3 w-3" />
            updated
          </dt>
          <dd className="mt-1 font-mono text-foreground truncate">
            {updatedLabel ?? "—"}
          </dd>
        </div>
      </dl>

      {bundle.sha256 && (
        <p className="mt-2 text-xs font-mono text-muted-foreground/70 truncate">
          sha256:{bundle.sha256.slice(0, 16)}...
        </p>
      )}

      <div className="mt-3 flex justify-end">
        <Button variant="outline" size="sm" onClick={() => onOpen(bundle.id)}>
          {canPublish ? "Manage bundle" : "View bundle"}
          <ArrowRight className="ml-1 h-3.5 w-3.5" />
        </Button>
      </div>
    </article>
  );
}
