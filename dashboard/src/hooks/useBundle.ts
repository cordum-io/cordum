import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import { queryKeys } from "../lib/queryKeys";
import type { Bundle } from "../api/generated/model/bundle";
import type { BundleVersion } from "../api/generated/model/bundleVersion";

/**
 * Bundle detail + versions + deployments hooks for the unified Bundle
 * Studio detail surface (Dashboard 5 step 4b). All three sit under the
 * `bundleStudio.*` queryKey tree so they coexist with the legacy
 * `policy-bundle*` cache used by `usePolicyBundle`.
 *
 * Default MSW handlers in `test-utils/handlers.ts` return safe empty
 * shapes so the page renders without per-test setup; tests override
 * with `server.use(...)` for the populated paths.
 */

export function useBundle(id: string) {
  return useQuery<Bundle>({
    queryKey: queryKeys.bundleStudio.detail(id),
    queryFn: () => get<Bundle>(`/policy/bundles/${encodeURIComponent(id)}`),
    enabled: Boolean(id),
    staleTime: 30_000,
  });
}

export function useBundleVersions(id: string) {
  return useQuery<{ items: BundleVersion[] }>({
    queryKey: queryKeys.bundleStudio.versions(id),
    queryFn: () =>
      get<{ items: BundleVersion[] }>(
        `/policy/bundles/${encodeURIComponent(id)}/versions`,
      ),
    enabled: Boolean(id),
    staleTime: 30_000,
  });
}

export interface BundleDeployment {
  scope: string;
  scope_kind?: string;
  scope_value?: string;
  version: string;
  active: boolean;
  deployed_at: string;
}

export function useBundleDeployments(id: string) {
  return useQuery<{ items: BundleDeployment[] }>({
    queryKey: queryKeys.bundleStudio.deployments(id),
    queryFn: () =>
      get<{ items: BundleDeployment[] }>(
        `/policy/bundles/${encodeURIComponent(id)}/deployments`,
      ),
    enabled: Boolean(id),
    staleTime: 30_000,
  });
}
