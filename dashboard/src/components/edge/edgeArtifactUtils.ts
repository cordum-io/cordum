import type { AgentActionEvent, EdgeArtifactPointer } from "@/api/types";

const DOWNLOAD_THRESHOLD_BYTES = 32 * 1024;

export function formatBytes(value?: number): string {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) return "unknown size";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${Math.round(value / 1024)} KB`;
  return `${(value / (1024 * 1024)).toFixed(1)} MB`;
}

export function artifactHref(uri: string): string | undefined {
  if (uri.startsWith("/api/") || uri.startsWith("/artifacts/")) return uri;
  return undefined;
}

export function isDownloadPreferred(artifact: EdgeArtifactPointer): boolean {
  return typeof artifact.sizeBytes === "number" && artifact.sizeBytes > DOWNLOAD_THRESHOLD_BYTES;
}

export function artifactsFromEvents(events: AgentActionEvent[]): EdgeArtifactPointer[] {
  const seen = new Set<string>();
  const artifacts: EdgeArtifactPointer[] = [];
  for (const event of events) {
    for (const artifact of event.artifactPtrs ?? []) {
      const key = `${artifact.sha256}:${artifact.uri}:${artifact.eventId}`;
      if (!seen.has(key)) {
        seen.add(key);
        artifacts.push(artifact);
      }
    }
  }
  return artifacts;
}

export function artifactDisplayName(artifact: EdgeArtifactPointer): string {
  return artifact.artifactType.replace(/^edge\./, "").replace(/[_-]/g, " ");
}
