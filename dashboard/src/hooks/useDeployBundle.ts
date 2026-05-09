import { useMutation, useQueryClient } from "@tanstack/react-query";
import { post } from "../api/client";
import { queryKeys } from "../lib/queryKeys";
import { logger } from "../lib/logger";
import { useToastStore } from "../state/toast";

export interface DeployBundleInput {
  bundleId: string;
  version: string;
  scope: { kind: string; value?: string };
  /**
   * Optional edge-mode override for the bundle's metadata applied
   * atomically with the deploy. Valid only when scope.kind starts with
   * `edge_`. Backend 5 propagates this onto Bundle.Metadata.EdgeMode if
   * set, leaving metadata untouched if undefined.
   */
  edge_mode?: "observe" | "enforce" | "enterprise-strict";
}

/**
 * Deploy a bundle version to a scope. Calls Backend 2's
 * `POST /api/v1/policy/bundles/:id/deploy` (worker-1ca4 PR #252).
 * Invalidates the bundle's deployments + active-deployment caches on success.
 */
export function useDeployBundle() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, DeployBundleInput>({
    mutationKey: ["deploy-bundle"],
    mutationFn: ({ bundleId, version, scope, edge_mode }) => {
      logger.info("bundle-studio", "Deploying bundle version", { bundleId, version, scope, edge_mode });
      return post<void>(
        `/policy/bundles/${encodeURIComponent(bundleId)}/deploy`,
        { version, scope, ...(edge_mode ? { edge_mode } : {}) },
      );
    },
    onSuccess: (_data, { bundleId, version, scope }) => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.bundleStudio.deployments(bundleId),
      });
      // Bundle.Metadata.EdgeMode may have changed atomically with the
      // deploy — invalidate the bundle detail too so the UI re-fetches.
      void queryClient.invalidateQueries({
        queryKey: queryKeys.bundleStudio.detail(bundleId),
      });
      useToastStore.getState().addToast({
        type: "success",
        title: `Promoted ${version}`,
        description: `Active for ${scope.kind}${scope.value ? `:${scope.value}` : ""}`,
      });
    },
    onError: (err, { bundleId, version }) => {
      logger.error("bundle-studio", "Deploy failed", { bundleId, version, err: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Promote failed", description: err.message });
    },
  });
}
