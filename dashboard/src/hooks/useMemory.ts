import { useQuery } from "@tanstack/react-query";
import { ApiError, get } from "../api/client";
import type {
  ArtifactPayload,
  JobArtifactRef,
  MemoryEntry,
  MemoryPayload,
} from "../api/types";

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return undefined;
  }
  return value as Record<string, unknown>;
}

function asString(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function asNumber(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) {
      return parsed;
    }
  }
  return undefined;
}

function normalizeRole(value: unknown): MemoryEntry["role"] {
  const normalized = asString(value).trim().toLowerCase();
  switch (normalized) {
    case "system":
    case "user":
    case "assistant":
    case "agent":
    case "tool":
      return normalized;
    default:
      return "unknown";
  }
}

function guessRoleFromKey(key: string): MemoryEntry["role"] {
  const lower = key.toLowerCase();
  if (lower.includes("prompt") || lower.includes("input")) return "user";
  if (lower.includes("result") || lower.includes("output")) return "assistant";
  return "system";
}

function stableStringify(value: unknown): string {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function parseTimestamp(value: unknown): string | undefined {
  const raw = asString(value).trim();
  return raw || undefined;
}

function mapMemoryEntryFromRecord(
  value: Record<string, unknown>,
  index: number,
): MemoryEntry | null {
  const content = asString(value.content).trim() || asString(value.message).trim();
  const role = normalizeRole(value.role);
  const timestamp =
    parseTimestamp(value.timestamp) ??
    parseTimestamp(value.created_at) ??
    parseTimestamp(value.updated_at);

  if (!content) return null;
  return {
    id: asString(value.id).trim() || `entry-${index + 1}`,
    role,
    content,
    timestamp,
    metadata: value,
  };
}

function mapMemoryEntries(raw: unknown): MemoryEntry[] {
  if (Array.isArray(raw)) {
    return raw
      .map((item, index) => {
        if (typeof item === "string") {
          const content = item.trim();
          if (!content) return null;
          return {
            id: `entry-${index + 1}`,
            role: "system" as const,
            content,
          };
        }
        const record = asRecord(item);
        if (!record) return null;
        return mapMemoryEntryFromRecord(record, index);
      })
      .filter((entry): entry is MemoryEntry => entry !== null);
  }

  const record = asRecord(raw);
  if (!record) return [];

  const messagesRaw = Array.isArray(record.messages)
    ? record.messages
    : Array.isArray(record.items)
      ? record.items
      : null;
  if (messagesRaw) {
    return mapMemoryEntries(messagesRaw);
  }

  const entries: MemoryEntry[] = [];
  Object.entries(record).forEach(([key, value], index) => {
    const content = stableStringify(value).trim();
    if (!content) return;
    entries.push({
      id: `entry-${index + 1}`,
      role: guessRoleFromKey(key),
      content,
      metadata: { field: key },
    });
  });
  return entries;
}

function attachMemoryEntries(payload: MemoryPayload): MemoryPayload {
  if (Array.isArray(payload.entries) && payload.entries.length > 0) {
    return payload;
  }
  return {
    ...payload,
    entries: mapMemoryEntries(payload.json),
  };
}

function mapArtifactRef(value: unknown): JobArtifactRef | null {
  if (typeof value === "string") {
    const ptr = value.trim();
    if (!ptr) return null;
    return { ptr, source: "job_artifacts" };
  }
  const record = asRecord(value);
  if (!record) return null;
  const ptr =
    asString(record.ptr).trim() ||
    asString(record.pointer).trim() ||
    asString(record.artifact_ptr).trim();
  if (!ptr) return null;
  const metadata = asRecord(record.metadata);
  const labels = asRecord(metadata?.labels);
  const timestamp =
    asString(record.timestamp).trim() ||
    asString(record.created_at).trim() ||
    asString(labels?.created_at).trim();
  return {
    ptr,
    contentType:
      asString(record.content_type).trim() ||
      asString(metadata?.content_type).trim() ||
      undefined,
    sizeBytes:
      asNumber(record.size_bytes) ??
      asNumber(metadata?.size_bytes),
    timestamp: timestamp || undefined,
    source: asString(record.source).trim() || "job_artifacts",
  };
}

function mapJobArtifactsResponse(raw: unknown): JobArtifactRef[] {
  if (Array.isArray(raw)) {
    return raw.map(mapArtifactRef).filter((item): item is JobArtifactRef => item !== null);
  }
  const record = asRecord(raw);
  if (!record) return [];
  const itemsRaw = Array.isArray(record.items)
    ? record.items
    : Array.isArray(record.artifacts)
      ? record.artifacts
      : null;
  if (!itemsRaw) return [];
  return itemsRaw.map(mapArtifactRef).filter((item): item is JobArtifactRef => item !== null);
}

function dedupeArtifacts(items: JobArtifactRef[]): JobArtifactRef[] {
  const seen = new Set<string>();
  const out: JobArtifactRef[] = [];
  for (const item of items) {
    const ptr = item.ptr.trim();
    if (!ptr || seen.has(ptr)) continue;
    seen.add(ptr);
    out.push({ ...item, ptr });
  }
  return out;
}

function fallbackArtifactsFromJobDetail(raw: unknown): JobArtifactRef[] {
  const record = asRecord(raw);
  if (!record) return [];

  const outputSafety = asRecord(record.output_safety);
  const updatedAt =
    asString(record.updated_at).trim() || asString(record.updatedAt).trim() || undefined;

  const refs: JobArtifactRef[] = [];
  const push = (ptrRaw: unknown, source: string) => {
    const ptr = asString(ptrRaw).trim();
    if (!ptr) return;
    refs.push({
      ptr,
      timestamp: updatedAt,
      source,
    });
  };

  push(record.result_ptr, "result");
  push(outputSafety?.original_ptr, "output_original");
  push(outputSafety?.redacted_ptr, "output_redacted");
  return dedupeArtifacts(refs);
}

export function useMemory(ptr?: string) {
  return useQuery<MemoryPayload, Error>({
    queryKey: ["memory", ptr],
    queryFn: async () => {
      const response = await get<MemoryPayload>(`/memory?ptr=${encodeURIComponent(ptr ?? "")}`);
      return attachMemoryEntries(response);
    },
    enabled: !!ptr,
    staleTime: 60_000,
    retry: (count, error) => {
      if (error instanceof ApiError && [404, 410].includes(error.status)) return false;
      return count < 2;
    },
  });
}

export function useArtifact(ptr?: string) {
  return useQuery<ArtifactPayload, Error>({
    queryKey: ["artifact", ptr],
    queryFn: async () => get<ArtifactPayload>(`/artifacts/${encodeURIComponent(ptr ?? "")}`),
    enabled: !!ptr,
    staleTime: 60_000,
    retry: (count, error) => {
      if (error instanceof ApiError && [404, 410].includes(error.status)) return false;
      return count < 2;
    },
  });
}

export function useJobArtifacts(jobId?: string) {
  return useQuery<JobArtifactRef[], Error>({
    queryKey: ["job-artifacts", jobId],
    queryFn: async () => {
      const id = (jobId ?? "").trim();
      if (!id) return [];
      const encodedID = encodeURIComponent(id);

      try {
        const response = await get<unknown>(`/jobs/${encodedID}/artifacts`);
        const mapped = mapJobArtifactsResponse(response);
        if (mapped.length > 0) {
          return dedupeArtifacts(mapped);
        }
      } catch (error) {
        if (!(error instanceof ApiError) || ![404, 405].includes(error.status)) {
          throw error;
        }
      }

      const detail = await get<unknown>(`/jobs/${encodedID}`);
      return fallbackArtifactsFromJobDetail(detail);
    },
    enabled: !!jobId,
    staleTime: 60_000,
    retry: (count, error) => {
      if (error instanceof ApiError && [404, 410].includes(error.status)) return false;
      return count < 2;
    },
  });
}

/** @internal exported for unit tests */
export const __memoryInternal = {
  mapMemoryEntries,
  attachMemoryEntries,
  mapJobArtifactsResponse,
  fallbackArtifactsFromJobDetail,
  dedupeArtifacts,
};
