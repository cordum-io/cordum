/*
 * EDGE-151-DASHBOARD — Hook for the Binary Integrity dashboard panel.
 *
 * Queries GET /api/v1/edge/binary-integrity/events on the gateway and
 * returns a typed list of binary-verify outcomes. Filters are server-side
 * to keep the wire bounded; the dashboard surface adds no client-side
 * narrowing on top.
 */

import { useMemo } from "react";
import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { get, ApiError } from "../api/client";

export type BinaryVerifyEventKind = "binary-verify-ok" | "binary-verify-fail";
export type BinaryVerifySigScheme = "gpg" | "codesign" | "authenticode" | "dev";

export interface BinaryVerifyEvent {
  event: BinaryVerifyEventKind;
  hash: string;
  path: string;
  sig_scheme: BinaryVerifySigScheme;
  fingerprint: string;
  reason: string;
  exit_code: number;
}

export interface BinaryVerifyListItem extends BinaryVerifyEvent {
  timestamp: string;
  tenant_id: string;
  endpoint?: string;
}

export interface BinaryVerifyListEnvelope {
  items: BinaryVerifyListItem[];
  next_cursor: string;
  returned: number;
}

export interface BinaryIntegrityFilters {
  event?: "ok" | "fail" | "";
  sigScheme?: BinaryVerifySigScheme | "";
  endpoint?: string;
  limit?: number;
  cursor?: string;
}

interface UseBinaryIntegrityEventsResult
  extends Omit<UseQueryResult<BinaryVerifyListEnvelope, ApiError>, "data"> {
  items: BinaryVerifyListItem[];
  nextCursor: string;
  hasNextPage: boolean;
  userMessage?: string;
}

function buildQueryString(filters: BinaryIntegrityFilters): string {
  const params = new URLSearchParams();
  if (filters.limit !== undefined && filters.limit > 0) {
    params.set("limit", String(filters.limit));
  }
  if (filters.cursor) {
    params.set("cursor", filters.cursor);
  }
  if (filters.event) {
    params.set("event", filters.event);
  }
  if (filters.sigScheme) {
    params.set("sig_scheme", filters.sigScheme);
  }
  if (filters.endpoint) {
    params.set("endpoint", filters.endpoint);
  }
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

function deriveUserMessage(err: unknown): string | undefined {
  if (!(err instanceof ApiError)) return undefined;
  if (err.status === 503) {
    return "Audit chain not installed — contact your operator. The binary-integrity panel requires the gateway's audit chainer (CORDUM_AUDIT_HMAC_KEY) to be configured.";
  }
  if (err.status === 403) {
    return "You don't have permission to read binary-integrity events.";
  }
  if (err.status === 400) {
    return "Filter validation failed; reset filters and try again.";
  }
  return `Failed to load binary-integrity events (${err.status}).`;
}

/**
 * useBinaryIntegrityEvents queries the gateway binary-integrity events
 * endpoint. Filter stability mirrors useAuditEvents — a parent re-render
 * with new-object-same-content filters does not refetch.
 */
export function useBinaryIntegrityEvents(
  filters: BinaryIntegrityFilters = {},
): UseBinaryIntegrityEventsResult {
  const stableFilters = useMemo<BinaryIntegrityFilters>(
    () => filters,
    [
      filters.event ?? "",
      filters.sigScheme ?? "",
      filters.endpoint ?? "",
      filters.cursor ?? "",
      filters.limit ?? 0,
    ],
  );

  const query = useQuery<BinaryVerifyListEnvelope, ApiError>({
    queryKey: ["edge-binary-integrity-events", stableFilters],
    queryFn: async () =>
      get<BinaryVerifyListEnvelope>(
        `/edge/binary-integrity/events${buildQueryString(stableFilters)}`,
      ),
    staleTime: 15_000,
  });

  const items = useMemo<BinaryVerifyListItem[]>(
    () => query.data?.items ?? [],
    [query.data],
  );

  const nextCursor = query.data?.next_cursor ?? "";
  const userMessage = deriveUserMessage(query.error);

  const { data: _unused, ...rest } = query;
  return {
    ...rest,
    items,
    nextCursor,
    hasNextPage: nextCursor !== "",
    userMessage,
  };
}
