import { useQuery, useMutation, useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { get, post, del } from "../api/client";
import type {
  ShadowPolicy,
  ShadowResultsSummary,
  ShadowComparisonsResponse,
  ShadowTimeseriesResponse,
  ShadowDiff,
} from "../api/types";

// Query-key prefix for all shadow-policy queries so BundleDetailPage /
// PromoteShadowDialog can do wide invalidations after a mutation.
export const SHADOW_POLICY_QUERY_KEY = "shadow-policy";

function bundleIDForPath(bundleID: string): string {
  // Bundle IDs contain "/" (e.g. "secops/safety"); the REST surface
  // tilde-encodes them.
  return encodeURIComponent(bundleID.replaceAll("/", "~"));
}

// useShadowPolicy fetches the current shadow policy for a bundle (or
// null when none is active). Null-safe: a 404 is translated into
// `data=null` so components can render the "no shadow" empty state
// without branching on isError.
export function useShadowPolicy(bundleID: string | undefined) {
  return useQuery<ShadowPolicy | null>({
    queryKey: [SHADOW_POLICY_QUERY_KEY, "detail", bundleID],
    queryFn: async () => {
      if (!bundleID) return null;
      try {
        return await get<ShadowPolicy>(
          `/policy/bundles/${bundleIDForPath(bundleID)}/shadow`,
        );
      } catch (err) {
        if (err instanceof Error && /\b404\b/.test(err.message)) return null;
        throw err;
      }
    },
    enabled: !!bundleID,
    staleTime: 30_000,
  });
}

export interface ActivateShadowInput {
  bundleID: string;
  content: string;
  metadata?: Record<string, string>;
}

export function useActivateShadow() {
  const qc = useQueryClient();
  return useMutation<ShadowPolicy, Error, ActivateShadowInput>({
    mutationFn: ({ bundleID, content, metadata }) =>
      post<ShadowPolicy>(
        `/policy/bundles/${bundleIDForPath(bundleID)}/shadow`,
        { content, metadata },
      ),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: [SHADOW_POLICY_QUERY_KEY, "detail", vars.bundleID] });
      qc.invalidateQueries({ queryKey: ["policy-bundle", vars.bundleID] });
      qc.invalidateQueries({ queryKey: ["policy-bundles"] });
    },
  });
}

export function useDeactivateShadow() {
  const qc = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (bundleID) =>
      del(`/policy/bundles/${bundleIDForPath(bundleID)}/shadow`),
    onSuccess: (_data, bundleID) => {
      qc.invalidateQueries({ queryKey: [SHADOW_POLICY_QUERY_KEY, "detail", bundleID] });
      qc.invalidateQueries({ queryKey: ["policy-bundle", bundleID] });
      qc.invalidateQueries({ queryKey: ["policy-bundles"] });
    },
  });
}

export interface ShadowResultsWindow {
  bundleID: string;
  fromMs?: number;
  untilMs?: number;
}

function windowQS({ fromMs, untilMs }: ShadowResultsWindow): string {
  const parts: string[] = [];
  if (fromMs != null) parts.push(`from=${fromMs}`);
  if (untilMs != null) parts.push(`until=${untilMs}`);
  return parts.length ? `?${parts.join("&")}` : "";
}

// useShadowResultsSummary fetches aggregate counts over the requested
// window. 60s staleTime matches the dashboard's time-range picker
// cadence; short enough to feel live, long enough to not thrash the
// backend while an operator is scrubbing.
export function useShadowResultsSummary(opts: ShadowResultsWindow | undefined) {
  return useQuery<ShadowResultsSummary>({
    queryKey: [SHADOW_POLICY_QUERY_KEY, "summary", opts?.bundleID, opts?.fromMs, opts?.untilMs],
    queryFn: () =>
      get<ShadowResultsSummary>(
        `/policy/bundles/${bundleIDForPath(opts!.bundleID)}/shadow/results/summary${windowQS(opts!)}`,
      ),
    enabled: !!opts?.bundleID,
    staleTime: 60_000,
  });
}

export interface ShadowComparisonsQuery extends ShadowResultsWindow {
  diff?: ShadowDiff;
  pageSize?: number;
}

// useShadowResultsComparisons is an infinite query over the
// comparisons endpoint. Each page's cursor feeds into the next
// fetchNextPage; truncated_at_max on the final page shuts off
// hasNextPage so the scroll loader can render a "showing most-recent
// N" marker.
export function useShadowResultsComparisons(opts: ShadowComparisonsQuery | undefined) {
  return useInfiniteQuery<ShadowComparisonsResponse>({
    queryKey: [
      SHADOW_POLICY_QUERY_KEY,
      "comparisons",
      opts?.bundleID,
      opts?.fromMs,
      opts?.untilMs,
      opts?.diff,
    ],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams();
      if (opts!.fromMs != null) params.set("from", String(opts!.fromMs));
      if (opts!.untilMs != null) params.set("until", String(opts!.untilMs));
      if (opts!.diff) params.set("diff", opts!.diff);
      if (opts!.pageSize) params.set("limit", String(opts!.pageSize));
      if (typeof pageParam === "string" && pageParam) params.set("cursor", pageParam);
      const qs = params.toString();
      return get<ShadowComparisonsResponse>(
        `/policy/bundles/${bundleIDForPath(opts!.bundleID)}/shadow/results/comparisons${qs ? `?${qs}` : ""}`,
      );
    },
    enabled: !!opts?.bundleID,
    initialPageParam: "",
    getNextPageParam: (last) => {
      if (!last || last.truncated_at_max) return undefined;
      return last.next_cursor || undefined;
    },
    staleTime: 30_000,
  });
}

export interface ShadowTimeseriesQuery extends ShadowResultsWindow {
  bucket: string; // e.g. "1m", "15m", "1h", "1d"
}

export function useShadowResultsTimeseries(opts: ShadowTimeseriesQuery | undefined) {
  return useQuery<ShadowTimeseriesResponse>({
    queryKey: [
      SHADOW_POLICY_QUERY_KEY,
      "timeseries",
      opts?.bundleID,
      opts?.fromMs,
      opts?.untilMs,
      opts?.bucket,
    ],
    queryFn: () => {
      const params = new URLSearchParams();
      if (opts!.fromMs != null) params.set("from", String(opts!.fromMs));
      if (opts!.untilMs != null) params.set("until", String(opts!.untilMs));
      params.set("bucket", opts!.bucket);
      return get<ShadowTimeseriesResponse>(
        `/policy/bundles/${bundleIDForPath(opts!.bundleID)}/shadow/results/timeseries?${params.toString()}`,
      );
    },
    enabled: !!opts?.bundleID && !!opts?.bucket,
    staleTime: 60_000,
  });
}
