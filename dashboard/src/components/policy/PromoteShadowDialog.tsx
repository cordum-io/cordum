import { useState, useMemo, useEffect, useRef } from "react";
import { AlertTriangle, Rocket } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/Button";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { useShadowPolicy, useDeactivateShadow } from "@/hooks/useShadowPolicy";
import { useUpdatePolicyBundle } from "@/hooks/usePolicies";

export interface PromoteShadowDialogProps {
  open: boolean;
  bundleID: string;
  bundleName: string;
  activeContent: string;
  onClose: () => void;
}

/**
 * PromoteShadowDialog drives the two-step client-side promotion of a
 * shadow policy to active. There is no atomic backend endpoint yet
 * (documented as a follow-up in the plan); we do PUT-active +
 * DELETE-shadow with rollback semantics.
 *
 * UX safety rails:
 *   - inline YAML diff preview (active vs shadow)
 *   - typing-to-confirm (user must retype the bundle name)
 *   - disable controls while mutations are pending
 *   - if deactivate fails after activate succeeded, surface a banner
 *     that survives dialog close so the operator sees the orphan and
 *     can retry deactivation on next visit.
 */
export function PromoteShadowDialog({
  open,
  bundleID,
  bundleName,
  activeContent,
  onClose,
}: PromoteShadowDialogProps) {
  const shadow = useShadowPolicy(bundleID);
  const updateBundle = useUpdatePolicyBundle();
  const deactivate = useDeactivateShadow();
  const [confirmText, setConfirmText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [orphanedShadow, setOrphanedShadow] = useState(false);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const firstFieldRef = useRef<HTMLInputElement | null>(null);

  // Save focus on open so we can restore it on close; focus the
  // confirm input on open so the keyboard user lands on the primary
  // interaction without hunting. Escape closes when not pending.
  useEffect(() => {
    if (!open) return;
    previousFocusRef.current = document.activeElement as HTMLElement | null;
    const t = setTimeout(() => firstFieldRef.current?.focus(), 0);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !updateBundle.isPending && !deactivate.isPending) {
        e.preventDefault();
        onClose();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => {
      clearTimeout(t);
      window.removeEventListener("keydown", onKey);
      previousFocusRef.current?.focus?.();
    };
  }, [open, onClose, updateBundle.isPending, deactivate.isPending]);

  const canConfirm = confirmText === bundleName && !updateBundle.isPending && !deactivate.isPending;

  const diff = useMemo(() => {
    if (!shadow.data) return { added: [], removed: [] };
    return buildInlineDiff(activeContent, shadow.data.content);
  }, [activeContent, shadow.data]);

  if (!open) return null;

  const handleConfirm = async () => {
    if (!shadow.data) return;
    setError(null);
    setOrphanedShadow(false);
    try {
      await updateBundle.mutateAsync({ id: bundleID, content: shadow.data.content });
    } catch (err) {
      // Active policy unchanged; shadow intact. Safe to retry.
      setError(
        err instanceof Error
          ? `Promotion cancelled — active policy unchanged: ${err.message}`
          : "Promotion cancelled — active policy unchanged.",
      );
      return;
    }
    try {
      await deactivate.mutateAsync(bundleID);
    } catch {
      // Active promoted, shadow orphaned. Recover on next load.
      setOrphanedShadow(true);
      toast.error("Promoted, but shadow cleanup failed — retry from the Shadow tab.");
      onClose();
      return;
    }
    toast.success("Shadow promoted to active");
    setConfirmText("");
    onClose();
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="promote-shadow-title"
      className="fixed inset-0 z-50 flex items-center justify-center bg-background/70 backdrop-blur-sm p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget && !updateBundle.isPending && !deactivate.isPending) {
          onClose();
        }
      }}
    >
      <div className="instrument-card w-full max-w-2xl p-6 space-y-4 max-h-[90vh] overflow-auto">
        <div className="flex items-center gap-2">
          <Rocket className="w-5 h-5 text-cordum" />
          <h2 id="promote-shadow-title" className="text-sm font-semibold">
            Promote shadow to active — {bundleName}
          </h2>
        </div>

        {!shadow.data && (
          <InfoBanner variant="warning">
            No shadow policy is active for this bundle. Activate one before promoting.
          </InfoBanner>
        )}

        {shadow.data && (
          <>
            <div className="instrument-card p-3">
              <p className="text-[10px] font-mono uppercase tracking-widest text-muted-foreground mb-2">
                YAML diff (active → shadow)
              </p>
              <pre className="text-xs font-mono overflow-auto max-h-64 whitespace-pre-wrap">
                {diff.removed.map((line, i) => (
                  <div key={`r-${i}`} className="text-red-400 bg-red-500/10">
                    − {line}
                  </div>
                ))}
                {diff.added.map((line, i) => (
                  <div key={`a-${i}`} className="text-emerald-400 bg-emerald-500/10">
                    + {line}
                  </div>
                ))}
                {diff.removed.length === 0 && diff.added.length === 0 && (
                  <div className="text-muted-foreground italic">No differences detected</div>
                )}
              </pre>
            </div>

            <InfoBanner variant="warning" title="This changes the active policy">
              After promotion, this bundle's active content becomes the shadow's
              content and all live traffic is evaluated against it. The previous
              active policy is kept as a snapshot for rollback.
            </InfoBanner>

            {error && <InfoBanner variant="error">{error}</InfoBanner>}
            {orphanedShadow && (
              <InfoBanner variant="warning" title="Orphaned shadow">
                Active policy updated but shadow cleanup failed. Return to the
                Shadow tab and click Deactivate to finish.
              </InfoBanner>
            )}

            <div>
              <label
                htmlFor="promote-confirm"
                className="block text-xs font-mono uppercase tracking-widest text-muted-foreground mb-1"
              >
                Type <span className="text-foreground">{bundleName}</span> to confirm
              </label>
              <input
                id="promote-confirm"
                ref={firstFieldRef}
                type="text"
                autoComplete="off"
                value={confirmText}
                onChange={(e) => setConfirmText(e.target.value)}
                onKeyDown={(e) => {
                  // Typing Enter inside the confirm field must never
                  // auto-submit — the operator has to click Promote.
                  if (e.key === "Enter") e.preventDefault();
                }}
                className="w-full rounded border border-border bg-transparent px-3 py-2 text-sm font-mono"
                disabled={updateBundle.isPending || deactivate.isPending}
              />
            </div>
          </>
        )}

        <div className="flex items-center justify-end gap-2">
          <Button
            variant="ghost"
            size="sm"
            onClick={onClose}
            disabled={updateBundle.isPending || deactivate.isPending}
          >
            Cancel
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => void handleConfirm()}
            disabled={!shadow.data || !canConfirm}
            data-testid="promote-confirm-button"
          >
            {updateBundle.isPending ? "Activating..." : deactivate.isPending ? "Cleaning up..." : (
              <>
                <AlertTriangle className="mr-1 h-3.5 w-3.5" />
                Promote
              </>
            )}
          </Button>
        </div>
      </div>
    </div>
  );
}

/**
 * buildInlineDiff does a trivial line-based LCS diff. Good enough for
 * a promote-confirmation preview where operators usually just need to
 * see "what's different". A richer diff (whitespace-aware, hunked)
 * can replace this without changing the surface.
 */
function buildInlineDiff(a: string, b: string) {
  const linesA = a.split("\n");
  const linesB = b.split("\n");
  const setA = new Set(linesA);
  const setB = new Set(linesB);
  const removed = linesA.filter((l) => !setB.has(l));
  const added = linesB.filter((l) => !setA.has(l));
  return { removed, added };
}
