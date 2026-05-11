import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import { queryKeys } from "../lib/queryKeys";
import type { BundleVersion } from "../api/generated/model/bundleVersion";

/**
 * Single bundle-version fetch for Dashboard 5 step-8 Diff tab. Backend 2's
 * `GET /api/v1/policy/bundles/:id/versions/:version` returns the
 * `BundleVersion` with `rule_snapshot[]` baked-in for tamper-evident
 * rollback semantics.
 */
export function useBundleVersion(bundleId: string, version: string) {
  return useQuery<BundleVersion>({
    queryKey: queryKeys.bundleStudio.version(bundleId, version),
    queryFn: () =>
      get<BundleVersion>(
        `/policy/bundles/${encodeURIComponent(bundleId)}/versions/${encodeURIComponent(version)}`,
      ),
    enabled: Boolean(bundleId) && Boolean(version),
    staleTime: 60_000,
  });
}
