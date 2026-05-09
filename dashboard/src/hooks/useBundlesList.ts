import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import { queryKeys } from "../lib/queryKeys";
import type { Bundle } from "../api/generated/model/bundle";

export interface BundlesListFilters {
  scope?: string;
  search?: string;
}

interface BundlesListResponse {
  items: Bundle[];
  total: number;
}

/**
 * List policy bundles for the unified Bundle Studio (Dashboard 5).
 * Uses the Backend-1.5 unified `Bundle` shape and queries
 * `/api/v1/policy/bundles` with optional `scope` + `search` filters.
 *
 * NOTE: cache key sits under `bundle-studio.list` to coexist with the legacy
 * `policy-bundles` key used by `usePolicyBundles` (which deserializes the
 * older `PolicyBundleSummary` shape). Backend 2 ships the unified-shape
 * endpoint; until it lands, the MSW default handler in
 * `test-utils/handlers.ts` returns an empty list so this hook can render
 * the empty state without per-test setup.
 */
export function useBundlesList(filters: BundlesListFilters = {}) {
  return useQuery<BundlesListResponse>({
    queryKey: queryKeys.bundleStudio.list(filters),
    queryFn: () => {
      const params = new URLSearchParams();
      if (filters.scope) params.set("scope", filters.scope);
      if (filters.search) params.set("search", filters.search);
      const qs = params.toString();
      return get<BundlesListResponse>(
        `/policy/bundles${qs ? `?${qs}` : ""}`,
      );
    },
    staleTime: 30_000,
  });
}
