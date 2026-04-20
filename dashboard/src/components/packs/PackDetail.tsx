import { useState } from "react";
import { CodeBlock } from "../ui/CodeBlock";
import {
  Package,
  X,
  ArrowLeft,
  Server,
  Shield,
  ShieldCheck,
  ShieldAlert,
  Settings,
  Activity,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  Loader2,
  ChevronDown,
  ChevronRight,
} from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { usePack, useVerifyPack } from "../../hooks/usePacks";
import type { Pack } from "../../api/types";
import type { PackVerifyResult, PackVerifyCheck } from "../../hooks/usePacks";

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
      return <Activity className="h-4 w-4 text-muted-foreground" />;
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
      <div className="rounded-2xl border border-border bg-card/60 p-4">
        {children}
      </div>
    </div>
  );
}

function KVRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-start justify-between gap-4 py-1.5 text-sm">
      <span className="shrink-0 text-muted-foreground">{label}</span>
      <span className="text-right text-ink break-all">{value}</span>
    </div>
  );
}

function VerificationCheckItem({ check }: { check: PackVerifyCheck }) {
  const [expanded, setExpanded] = useState(false);
  const passed = check.status === "pass";
  const hasDetails = !!(check.message || check.details);

  return (
    <div className="py-2">
      <button
        type="button"
        className="flex w-full items-center gap-2 text-left text-sm"
        onClick={() => hasDetails && setExpanded(!expanded)}
        disabled={!hasDetails}
      >
        {passed ? (
          <CheckCircle2 className="h-4 w-4 shrink-0 text-success" />
        ) : (
          <XCircle className="h-4 w-4 shrink-0 text-danger" />
        )}
        <span className="flex-1 text-ink">{check.name}</span>
        {hasDetails && (
          expanded ? (
            <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
          )
        )}
      </button>
      {expanded && hasDetails && (
        <div className="ml-6 mt-1 text-xs text-muted-foreground">
          {check.message && <p>{check.message}</p>}
          {check.details && (
            <div className="mt-1">
              <CodeBlock language="text" maxHeight={150}>{check.details}</CodeBlock>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function VerificationResults({ result }: { result: PackVerifyResult }) {
  const verified = result.overall === "verified";
  const passCount = result.checks.filter((c) => c.status === "pass").length;

  return (
    <Section title="Verification" icon={verified ? ShieldCheck : ShieldAlert}>
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            {verified ? (
              <Badge variant="success">Verified</Badge>
            ) : (
              <Badge variant="danger">Failed</Badge>
            )}
            <span className="text-xs text-muted-foreground">
              {passCount}/{result.checks.length} checks passed
            </span>
          </div>
          <span className="text-xs text-muted-foreground">
            {new Date(result.verified_at).toLocaleString()}
          </span>
        </div>
        <div className="divide-y divide-border">
          {result.checks.map((check, i) => (
            <VerificationCheckItem key={`${check.name}-${i}`} check={check} />
          ))}
        </div>
      </div>
    </Section>
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
  const verify = useVerifyPack();
  const [verifyResult, setVerifyResult] = useState<PackVerifyResult | null>(null);

  if (isLoading && !pack) {
    return (
      <div className="space-y-6">
        <div className="flex items-center gap-3">
          <button type="button" onClick={onClose} className="rounded-full p-1.5 hover:bg-surface2">
            <ArrowLeft className="h-4 w-4 text-muted-foreground" />
          </button>
          <span className="text-sm text-muted-foreground">Loading...</span>
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
          <button type="button" onClick={onClose} className="rounded-full p-1.5 hover:bg-surface2">
            <ArrowLeft className="h-4 w-4 text-muted-foreground" />
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
          <button type="button" onClick={onClose} className="rounded-full p-1.5 hover:bg-surface2">
            <ArrowLeft className="h-4 w-4 text-muted-foreground" />
          </button>
          <Package className="h-6 w-6 text-accent" />
          <div>
            <h2 className="font-display text-lg font-bold text-ink">{pack.name}</h2>
            <span className="text-xs text-muted-foreground">v{pack.version}</span>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Badge variant={statusVariant(pack.status)}>{pack.status}</Badge>
          <button type="button" onClick={onClose} className="rounded-full p-1.5 hover:bg-surface2">
            <X className="h-4 w-4 text-muted-foreground" />
          </button>
        </div>
      </div>

      {/* Manifest */}
      <Section title="Manifest" icon={Package}>
        {manifestEntries.length === 0 ? (
          <p className="text-xs text-muted-foreground">No manifest data available.</p>
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
          <p className="text-xs text-muted-foreground">No configuration set.</p>
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
          <p className="text-xs text-muted-foreground">No capabilities declared.</p>
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
          <p className="text-xs text-muted-foreground">No pool assigned.</p>
        )}
      </Section>

      {/* Health Check Status */}
      <Section title="Health Check" icon={Activity}>
        <div className="flex items-center gap-2">
          <HealthIcon status={pack.status} />
          <span className="text-sm text-ink capitalize">{pack.status}</span>
        </div>
      </Section>

      {/* Verify */}
      <div className="flex items-center gap-3">
        <Button
          variant="outline"
          size="sm"
          disabled={verify.isPending}
          title="Validates schema, file integrity, overlay compatibility, and resource declarations."
          onClick={() => {
            verify.mutate(packId, {
              onSuccess: (result) => setVerifyResult(result),
            });
          }}
        >
          {verify.isPending ? (
            <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
          ) : (
            <ShieldCheck className="mr-1.5 h-3.5 w-3.5" />
          )}
          {verify.isPending ? "Verifying..." : "Verify Integrity"}
        </Button>
        {verifyResult && (
          <span className="text-xs text-muted-foreground">
            Last verified: {new Date(verifyResult.verified_at).toLocaleString()}
          </span>
        )}
      </div>

      {/* Verification Results */}
      {verifyResult && <VerificationResults result={verifyResult} />}

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
