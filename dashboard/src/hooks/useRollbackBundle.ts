import { useMutation, useQueryClient } from "@tanstack/react-query";
import { post } from "../api/client";
import { queryKeys } from "../lib/queryKeys";
import { logger } from "../lib/logger";
import { useToastStore } from "../state/toast";

export interface RollbackBundleInput {
  bundleId: string;
  scope: { kind: string; value?: string };
}

/**
 * Rollback the previous bundle deployment for a scope. Calls Backend 2's
 * `POST /api/v1/policy/bundles/:id/rollback` (worker-1ca4 PR #252) which
 * pops the latest deployment record off the scope's history list and
 * re-activates the prior version. Returns 409 if no rollback target.
 */
export function useRollbackBundle() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, RollbackBundleInput>({
    mutationKey: ["rollback-bundle"],
    mutationFn: ({ bundleId, scope }) => {
      logger.info("bundle-studio", "Rolling back bundle deployment", { bundleId, scope });
      return post<void>(
        `/policy/bundles/${encodeURIComponent(bundleId)}/rollback`,
        { scope },
      );
    },
    onSuccess: (_data, { bundleId, scope }) => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.bundleStudio.deployments(bundleId),
      });
      useToastStore.getState().addToast({
        type: "success",
        title: "Rollback complete",
        description: `${scope.kind}${scope.value ? `:${scope.value}` : ""}`,
      });
    },
    onError: (err, { bundleId }) => {
      logger.error("bundle-studio", "Rollback failed", { bundleId, err: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Rollback failed", description: err.message });
    },
  });
}
