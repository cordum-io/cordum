import { Download, ExternalLink, FileText, PackageCheck } from "lucide-react";
import type { AgentActionEvent, EdgeArtifactPointer } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { useExportEdgeSession } from "@/hooks/useEdgeSessions";
import {
  artifactDisplayName,
  artifactHref,
  artifactsFromEvents,
  formatBytes,
  isDownloadPreferred,
} from "./edgeArtifactUtils";

interface EdgeArtifactsPanelProps {
  sessionId: string;
  events?: AgentActionEvent[];
  artifacts?: EdgeArtifactPointer[];
  onViewArtifact?: (artifact: EdgeArtifactPointer) => void;
}

export function EdgeArtifactsPanel({
  sessionId,
  events = [],
  artifacts = [],
  onViewArtifact,
}: EdgeArtifactsPanelProps) {
  const exportMutation = useExportEdgeSession();
  const eventArtifacts = artifactsFromEvents(events);
  const allArtifacts = mergeArtifacts([...artifacts, ...eventArtifacts, ...(exportMutation.data?.artifacts ?? [])]);

  return (
    <section className="rounded-3xl border border-border bg-surface-1/70 p-4">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p className="text-xs font-medium uppercase tracking-[0.2em] text-cordum">Artifacts</p>
          <h3 className="mt-1 text-lg font-semibold text-foreground">Evidence pointers</h3>
          <p className="mt-2 max-w-2xl text-sm text-muted-foreground">
            Pointer metadata only. Evidence contents stay behind approved export or download flows.
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          loading={exportMutation.isPending}
          onClick={() => exportMutation.mutate({ sessionId, request: { maxEvents: 500 } })}
        >
          <PackageCheck className="h-3.5 w-3.5" />
          Export evidence
        </Button>
      </header>

      {exportMutation.data && (
        <div className="mt-4 rounded-2xl border border-cordum/20 bg-cordum/10 p-3 text-sm text-foreground">
          Export ready: {exportMutation.data.manifestVersion} generated {exportMutation.data.generatedAt}
        </div>
      )}
      {exportMutation.error && (
        <div className="mt-4 rounded-2xl border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
          Export failed: {exportMutation.error.message}
        </div>
      )}

      {allArtifacts.length === 0 ? (
        <div className="mt-4">
          <EmptyState title="No artifact pointers" description="This session has not emitted Edge artifact pointers yet." />
        </div>
      ) : (
        <div className="mt-4 grid gap-3">
          {allArtifacts.map((artifact) => (
            <ArtifactRow
              key={`${artifact.sha256}:${artifact.uri}:${artifact.eventId}`}
              artifact={artifact}
              onViewArtifact={onViewArtifact}
            />
          ))}
        </div>
      )}
    </section>
  );
}

function ArtifactRow({
  artifact,
  onViewArtifact,
}: {
  artifact: EdgeArtifactPointer;
  onViewArtifact?: (artifact: EdgeArtifactPointer) => void;
}) {
  const href = artifactHref(artifact.uri);
  const actionLabel = isDownloadPreferred(artifact) ? "Download" : "View pointer";
  return (
    <article className="rounded-2xl border border-border bg-surface-0 p-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <FileText className="h-4 w-4 text-cordum" />
            <h4 className="font-medium text-foreground">{artifactDisplayName(artifact)}</h4>
            <StatusBadge variant="muted">{artifact.redactionLevel}</StatusBadge>
            <StatusBadge variant="info">{artifact.retentionClass}</StatusBadge>
          </div>
          <dl className="mt-3 grid gap-2 text-xs sm:grid-cols-2">
            <Meta label="SHA-256" value={artifact.sha256} />
            <Meta label="Size" value={formatBytes(artifact.sizeBytes)} />
            <Meta label="Linked event" value={artifact.eventId} />
            <Meta label="Created" value={artifact.createdAt} />
            <Meta label="Content type" value={artifact.contentType} />
            <Meta label="Pointer" value={artifact.uri || "metadata only"} />
          </dl>
        </div>
        {href ? (
          <a
            href={href}
            className="inline-flex h-8 items-center gap-1.5 rounded-xl border border-border px-3 text-xs font-medium text-foreground hover:bg-secondary"
          >
            <Download className="h-3.5 w-3.5" />
            {actionLabel}
          </a>
        ) : (
          <Button
            variant="outline"
            size="sm"
            disabled={!onViewArtifact}
            onClick={() => onViewArtifact?.(artifact)}
          >
            <ExternalLink className="h-3.5 w-3.5" />
            Pointer only
          </Button>
        )}
      </div>
    </article>
  );
}

function Meta({ label, value }: { label: string; value?: string }) {
  if (!value) return null;
  return (
    <div className="min-w-0">
      <dt className="text-[10px] uppercase tracking-[0.16em] text-muted-foreground">{label}</dt>
      <dd className="mt-1 break-all font-mono text-foreground">{value}</dd>
    </div>
  );
}

function mergeArtifacts(artifacts: EdgeArtifactPointer[]): EdgeArtifactPointer[] {
  const seen = new Set<string>();
  return artifacts.filter((artifact) => {
    const key = `${artifact.sha256}:${artifact.uri}:${artifact.eventId}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}
