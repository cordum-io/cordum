import { useMutation, useQueryClient } from "@tanstack/react-query";
import { post } from "../api/client";
import { queryKeys } from "../lib/queryKeys";
import { logger } from "../lib/logger";
import { useToastStore } from "../state/toast";

export interface DeployBundleInput {
  bundleId: string;
  version: string;
  scope: { kind: string; value?: string };
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
    mutationFn: ({ bundleId, version, scope }) => {
      logger.info("bundle-studio", "Deploying bundle version", { bundleId, version, scope });
      return post<void>(
        `/policy/bundles/${encodeURIComponent(bundleId)}/deploy`,
        { version, scope },
      );
    },
    onSuccess: (_data, { bundleId, version, scope }) => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.bundleStudio.deployments(bundleId),
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
