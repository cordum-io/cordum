import { useQuery } from "@tanstack/react-query";
import { getArtifact, getGetArtifactQueryKey } from "@/api/generated/memory/memory";
import type { ArtifactDetail } from "@/api/generated/model/artifactDetail";
import { logger } from "@/lib/logger";

const STALE_MS = 60_000;

export interface UseArtifactResult {
  data: string;
  artifact: ArtifactDetail | undefined;
  loading: boolean;
  error: Error | null;
}

/**
 * Fetches artifact contents by `Decision.input_ref` / `output_ref`. The
 * gateway returns ArtifactDetail with `content_base64`; this hook decodes
 * to a UTF-8 string for display in a CodeBlock. When `ref` is undefined
 * or empty, the hook short-circuits with an empty result so callers can
 * render an "Awaiting Backend 3/4 backfill" placeholder without firing a
 * spurious request.
 *
 * 60s staleTime — artifact bodies are immutable for a given ptr, so we
 * cache aggressively and avoid refetch during a single drawer session.
 */
export function useArtifact(ref: string | undefined): UseArtifactResult {
  const enabled = typeof ref === "string" && ref.length > 0;
  const query = useQuery({
    queryKey: getGetArtifactQueryKey(ref ?? ""),
    queryFn: ({ signal }) => getArtifact(ref ?? "", signal),
    enabled,
    staleTime: STALE_MS,
    retry: false,
  });

  return {
    data: enabled ? decodeContent(query.data) : "",
    artifact: query.data,
    loading: enabled ? query.isPending : false,
    error: query.error instanceof Error ? query.error : null,
  };
}

function decodeContent(detail: ArtifactDetail | undefined): string {
  if (!detail?.content_base64) return "";
  try {
    // atob produces a binary string; for UTF-8 artifacts we decode via
    // TextDecoder for correctness (not all artifact bytes are ASCII).
    const binary = atob(detail.content_base64);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    return new TextDecoder("utf-8", { fatal: false }).decode(bytes);
  } catch (err) {
    logger.warn("artifact", "Failed to decode artifact content_base64", {
      err: String(err),
    });
    return "";
  }
}
