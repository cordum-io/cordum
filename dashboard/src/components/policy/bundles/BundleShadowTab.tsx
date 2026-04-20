import { useCallback, useEffect, useState } from "react";
import { CopyCheck, Rocket, AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { BundleYamlEditor } from "./BundleYamlEditor";
import {
  useShadowPolicy,
  useActivateShadow,
  useDeactivateShadow,
} from "@/hooks/useShadowPolicy";
import { formatRelativeTime } from "@/lib/utils";
import { ShadowImpactPanel } from "@/components/policy/ShadowImpactPanel";

interface BundleShadowTabProps {
  bundleID: string;
  activeContent: string;
  canEdit: boolean;
  onOpenPromote?: () => void;
}

/**
 * BundleShadowTab implements the "Shadow" section on the bundle detail
 * page. Two primary states:
 *   - NO SHADOW: YAML editor pre-filled with the active bundle's
 *     content + "Activate shadow" button that posts the draft.
 *   - SHADOW ACTIVE: read-only view of the stored shadow with
 *     metadata, the impact panel, and a "Deactivate" / "Promote to
 *     active" action pair. Promote opens a dialog owned by the parent
 *     page via `onOpenPromote`.
 *
 * Activation is admin-only upstream; the tab still renders for
 * read-only viewers with an info banner explaining the restriction.
 */
export function BundleShadowTab({
  bundleID,
  activeContent,
  canEdit,
  onOpenPromote,
}: BundleShadowTabProps) {
  const { data: shadow, isLoading, isError, error, refetch } = useShadowPolicy(bundleID);
  const activate = useActivateShadow();
  const deactivate = useDeactivateShadow();

  const [draft, setDraft] = useState<string | null>(null);
  const [showDeactivateConfirm, setShowDeactivateConfirm] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  // Prime the editor with the active content when the tab mounts for a
  // bundle without a shadow. Re-runs if the user switches bundles.
  useEffect(() => {
    setDraft(null);
    setLocalError(null);
  }, [bundleID]);

  const currentDraft = draft ?? activeContent;

  const handleActivate = useCallback(async () => {
    if (!bundleID || !currentDraft.trim()) return;
    setLocalError(null);
    try {
      await activate.mutateAsync({ bundleID, content: currentDraft });
      setDraft(null);
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : "Activate failed");
    }
  }, [bundleID, currentDraft, activate]);

  const handleDeactivate = useCallback(async () => {
    setLocalError(null);
    try {
      await deactivate.mutateAsync(bundleID);
      setShowDeactivateConfirm(false);
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : "Deactivate failed");
    }
  }, [bundleID, deactivate]);

  if (isLoading) {
    return (
      <div className="space-y-4" data-testid="shadow-loading">
        <SkeletonCard />
        <SkeletonCard />
      </div>
    );
  }

  if (isError) {
    return (
      <ErrorBanner
        message={error instanceof Error ? error.message : "Failed to load shadow policy"}
        onRetry={() => void refetch()}
      />
    );
  }

  if (!canEdit && !shadow) {
    return (
      <EmptyState
        icon={<CopyCheck className="w-6 h-6" />}
        title="No shadow policy active"
        description="Shadow policy evaluation requires admin access to activate. Ask an operator to enable it for this bundle."
      />
    );
  }

  if (!shadow) {
    // No shadow active — render the activation editor.
    return (
      <div className="space-y-4" data-testid="shadow-absent">
        <InfoBanner variant="info" title="Shadow evaluation is off">
          Activate a candidate policy to evaluate it against live traffic without
          enforcement. Shadow results appear alongside — they never change the
          active verdict.
        </InfoBanner>

        <BundleYamlEditor
          yaml={currentDraft}
          editable={canEdit}
          onChange={setDraft}
        />

        {localError && (
          <InfoBanner variant="error" title="Activation failed">
            {localError}
          </InfoBanner>
        )}

        <div className="flex items-center justify-between">
          <p className="text-xs text-muted-foreground">
            The draft starts as a clone of the active bundle — edit it to try a
            proposed change.
          </p>
          {canEdit && (
            <Button
              variant="outline"
              size="sm"
              disabled={!currentDraft.trim() || activate.isPending}
              onClick={() => void handleActivate()}
              data-testid="shadow-activate-button"
            >
              <CopyCheck className="mr-1 h-3.5 w-3.5" />
              {activate.isPending ? "Activating..." : "Activate shadow"}
            </Button>
          )}
        </div>
      </div>
    );
  }

  // Shadow is active: show metadata, impact panel, and deactivate/promote.
  return (
    <div className="space-y-6" data-testid="shadow-present">
      <div className="instrument-card p-4 space-y-2">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <CopyCheck className="w-4 h-4 text-cordum" />
            <span className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
              Shadow active
            </span>
          </div>
          <div className="flex items-center gap-2">
            {canEdit && (
              <>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={!onOpenPromote}
                  onClick={onOpenPromote}
                  data-testid="shadow-promote-button"
                >
                  <Rocket className="mr-1 h-3.5 w-3.5" />
                  Promote to active
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={deactivate.isPending}
                  onClick={() => setShowDeactivateConfirm(true)}
                  data-testid="shadow-deactivate-button"
                >
                  Deactivate
                </Button>
              </>
            )}
          </div>
        </div>
        <div className="grid grid-cols-2 gap-3 text-xs font-mono sm:grid-cols-4">
          <Metadatum label="Shadow ID" value={shadow.shadow_bundle_id} mono />
          <Metadatum
            label="Activated"
            value={formatRelativeTime(shadow.activated_at)}
          />
          <Metadatum
            label="Created"
            value={formatRelativeTime(shadow.created_at)}
          />
          <Metadatum label="By" value={shadow.created_by ?? "—"} />
        </div>
      </div>

      {localError && (
        <InfoBanner variant="error" title="Shadow mutation failed">
          {localError}
        </InfoBanner>
      )}

      <ShadowImpactPanel bundleID={bundleID} />

      <details className="instrument-card p-4">
        <summary className="text-xs font-mono uppercase tracking-widest text-muted-foreground cursor-pointer">
          Shadow YAML (read-only)
        </summary>
        <div className="mt-3">
          <BundleYamlEditor
            yaml={shadow.content}
            editable={false}
            onChange={() => {}}
          />
        </div>
      </details>

      {showDeactivateConfirm && (
        <div
          role="dialog"
          aria-modal="true"
          aria-labelledby="shadow-deactivate-title"
          className="fixed inset-0 z-50 flex items-center justify-center bg-background/70 backdrop-blur-sm"
        >
          <div className="instrument-card w-full max-w-md p-6 space-y-4">
            <div className="flex items-center gap-2">
              <AlertTriangle className="w-5 h-5 text-amber-400" />
              <h2 id="shadow-deactivate-title" className="text-sm font-semibold">
                Deactivate shadow evaluation?
              </h2>
            </div>
            <p className="text-sm text-muted-foreground">
              This removes the candidate policy from live evaluation. Historical
              shadow results stay available in the audit chain but new results
              will stop flowing.
            </p>
            <div className="flex items-center justify-end gap-2">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setShowDeactivateConfirm(false)}
                disabled={deactivate.isPending}
              >
                Cancel
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() => void handleDeactivate()}
                disabled={deactivate.isPending}
              >
                {deactivate.isPending ? "Deactivating..." : "Deactivate"}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function Metadatum({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div>
      <p className="text-[10px] uppercase tracking-wider text-muted-foreground">{label}</p>
      <p className={mono ? "truncate text-foreground" : "text-foreground"} title={value}>
        {value}
      </p>
    </div>
  );
}
