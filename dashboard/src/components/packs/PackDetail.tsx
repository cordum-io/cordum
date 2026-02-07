import {
  Package,
  X,
  ArrowLeft,
  Server,
  Shield,
  Settings,
  Activity,
  CheckCircle2,
  XCircle,
  AlertTriangle,
} from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { usePack } from "../../hooks/usePacks";
import type { Pack } from "../../api/types";

function statusVariant(
  status: string,
): "success" | "warning" | "danger" | "default" {
  switch (status) {
    case "active":
    case "running":
    case "healthy":
      return "success";
    case "installing":
    case "updating":
    case "degraded":
      return "warning";
    case "error":
    case "failed":
    case "unhealthy":
      return "danger";
    default:
      return "default";
  }
}

function HealthIcon({ status }: { status: string }) {
  switch (status) {
    case "healthy":
    case "active":
    case "running":
      return <CheckCircle2 className="h-4 w-4 text-success" />;
    case "degraded":
    case "warning":
      return <AlertTriangle className="h-4 w-4 text-warning" />;
    case "unhealthy":
    case "error":
    case "failed":
      return <XCircle className="h-4 w-4 text-danger" />;
    default:
      return <Activity className="h-4 w-4 text-muted" />;
  }
}

function Section({
  title,
  icon: Icon,
  children,
}: {
  title: string;
  icon: React.ComponentType<{ className?: string }>;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 text-sm font-semibold text-ink">
        <Icon className="h-4 w-4 text-accent" />
        {title}
      </div>
      <div className="rounded-2xl border border-border bg-white/60 p-4">
        {children}
      </div>
    </div>
  );
}

function KVRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-start justify-between gap-4 py-1.5 text-sm">
      <span className="shrink-0 text-muted">{label}</span>
      <span className="text-right text-ink break-all">{value}</span>
    </div>
  );
}

interface PackDetailProps {
  packId: string;
  pack?: Pack;
  onClose: () => void;
}

export default function PackDetail({ packId, pack: prefetched, onClose }: PackDetailProps) {
  const { data: fetched, isLoading, error } = usePack(packId);
  const pack = prefetched ?? fetched;

  if (isLoading && !pack) {
    return (
      <div className="space-y-6">
        <div className="flex items-center gap-3">
          <button onClick={onClose} className="rounded-full p-1.5 hover:bg-surface2">
            <ArrowLeft className="h-4 w-4 text-muted" />
          </button>
          <span className="text-sm text-muted">Loading...</span>
        </div>
        <div className="space-y-4">
          {[1, 2, 3].map((i) => (
            <div key={i} className="h-24 animate-pulse rounded-2xl bg-surface2" />
          ))}
        </div>
      </div>
    );
  }

  if (error || !pack) {
    return (
      <div className="space-y-6">
        <div className="flex items-center gap-3">
          <button onClick={onClose} className="rounded-full p-1.5 hover:bg-surface2">
            <ArrowLeft className="h-4 w-4 text-muted" />
          </button>
          <span className="text-sm text-danger">
            Failed to load pack{error instanceof Error ? `: ${error.message}` : ""}
          </span>
        </div>
      </div>
    );
  }

  const manifest = pack.manifest ?? {};
  const config = pack.config ?? {};
  const manifestEntries = Object.entries(manifest);
  const configEntries = Object.entries(config);

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-3">
          <button onClick={onClose} className="rounded-full p-1.5 hover:bg-surface2">
            <ArrowLeft className="h-4 w-4 text-muted" />
          </button>
          <Package className="h-6 w-6 text-accent" />
          <div>
            <h2 className="font-display text-lg font-bold text-ink">{pack.name}</h2>
            <span className="text-xs text-muted">v{pack.version}</span>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Badge variant={statusVariant(pack.status)}>{pack.status}</Badge>
          <button onClick={onClose} className="rounded-full p-1.5 hover:bg-surface2">
            <X className="h-4 w-4 text-muted" />
          </button>
        </div>
      </div>

      {/* Manifest */}
      <Section title="Manifest" icon={Package}>
        {manifestEntries.length === 0 ? (
          <p className="text-xs text-muted">No manifest data available.</p>
        ) : (
          <div className="divide-y divide-border">
            {manifestEntries.map(([key, value]) => (
              <KVRow
                key={key}
                label={key}
                value={typeof value === "string" ? value : JSON.stringify(value)}
              />
            ))}
          </div>
        )}
      </Section>

      {/* Config */}
      <Section title="Configuration" icon={Settings}>
        {configEntries.length === 0 ? (
          <p className="text-xs text-muted">No configuration set.</p>
        ) : (
          <div className="divide-y divide-border">
            {configEntries.map(([key, value]) => (
              <KVRow
                key={key}
                label={key}
                value={typeof value === "string" ? value : JSON.stringify(value)}
              />
            ))}
          </div>
        )}
      </Section>

      {/* Capabilities */}
      <Section title="Capabilities" icon={Shield}>
        {pack.capabilities.length === 0 ? (
          <p className="text-xs text-muted">No capabilities declared.</p>
        ) : (
          <div className="flex flex-wrap gap-2">
            {pack.capabilities.map((cap) => (
              <Badge key={cap} variant="info">
                {cap}
              </Badge>
            ))}
          </div>
        )}
      </Section>

      {/* Pool Assignment */}
      <Section title="Pool Assignment" icon={Server}>
        {pack.poolAssignment ? (
          <div className="flex items-center gap-2">
            <Server className="h-4 w-4 text-accent" />
            <span className="text-sm font-medium text-ink">{pack.poolAssignment}</span>
          </div>
        ) : (
          <p className="text-xs text-muted">No pool assigned.</p>
        )}
      </Section>

      {/* Health Check Status */}
      <Section title="Health Check" icon={Activity}>
        <div className="flex items-center gap-2">
          <HealthIcon status={pack.status} />
          <span className="text-sm text-ink capitalize">{pack.status}</span>
        </div>
      </Section>

      {/* Back to list */}
      <div className="border-t border-border pt-4">
        <Button variant="ghost" size="sm" onClick={onClose}>
          <ArrowLeft className="mr-1 h-3.5 w-3.5" />
          Back to Packs
        </Button>
      </div>
    </div>
  );
}
