import { useState } from "react";
import { Loader2 } from "lucide-react";
import { Drawer } from "@/components/ui/Drawer";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { EmptyState } from "@/components/ui/EmptyState";
import { useBundlesList } from "@/hooks/useBundlesList";
import {
  useAddRuleToBundle,
  type AddRuleToBundleResult,
} from "@/hooks/useAddRuleToBundle";

interface PublishToBundleModalProps {
  ruleId: string;
  open: boolean;
  onClose: () => void;
  onSuccess?: () => void;
}

type Feedback =
  | { kind: "idle" }
  | { kind: "error"; message: string }
  | { kind: "rule_not_found"; ruleId: string }
  | { kind: "bundle_not_found"; bundleId: string };

/**
 * Phase 3E publish-to-bundle modal. Lists active bundles via
 * `useBundlesList`, lets the author pick one, gates the bind behind a
 * `ConfirmDialog`, then calls `useAddRuleToBundle` (POST
 * `/api/v1/policy/bundles/{id}/rules`).
 *
 * 404 disambiguation: the hook surfaces `kind: "rule_not_found"` vs
 * `"bundle_not_found"` so the modal renders the right copy without
 * re-fetching. `bundle_not_found` typically means the bundle was
 * deleted between list-fetch and submit; `rule_not_found` means the
 * caller's rule id never reached `Backend 5c.CreateRule` — usually a
 * sign that Save-draft hasn't been clicked yet.
 *
 * Create-new-bundle path is intentionally NOT implemented here. Backend
 * 5c ships rule writes + add-to-bundle but no `POST /policy/bundles`
 * shape exists yet (a new bundle requires version + scope + metadata
 * which is out-of-scope for D3E). Surface a disabled link with tooltip
 * pointing to Bundle Studio's "+ New bundle" affordance instead.
 */
export default function PublishToBundleModal({
  ruleId,
  open,
  onClose,
  onSuccess,
}: PublishToBundleModalProps) {
  const bundlesQ = useBundlesList();
  const addRule = useAddRuleToBundle();

  const [selected, setSelected] = useState<string>("");
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [feedback, setFeedback] = useState<Feedback>({ kind: "idle" });

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!selected) return;
    setFeedback({ kind: "idle" });
    setConfirmOpen(true);
  }

  function handleConfirm() {
    addRule.mutate(
      { bundleId: selected, ruleId },
      {
        onSuccess: (result: AddRuleToBundleResult) => {
          setConfirmOpen(false);
          if (result.ok) {
            setFeedback({ kind: "idle" });
            onSuccess?.();
            onClose();
            return;
          }
          if (result.kind === "bundle_not_found") {
            setFeedback({ kind: "bundle_not_found", bundleId: selected });
            return;
          }
          if (result.kind === "rule_not_found") {
            setFeedback({ kind: "rule_not_found", ruleId });
            return;
          }
          setFeedback({ kind: "error", message: result.error });
        },
      },
    );
  }

  return (
    <>
      <Drawer
        open={open}
        onClose={onClose}
        size="md"
        label={`Publish ${ruleId} to bundle`}
      >
        <form onSubmit={handleSubmit} className="flex h-full flex-col">
          <header className="border-b border-border px-5 py-4">
            <h2 className="text-base font-semibold text-foreground">
              Publish to bundle
            </h2>
            <p className="mt-1 text-sm text-muted-foreground">
              Add{" "}
              <code className="font-mono text-xs">{ruleId}</code> to a bundle&rsquo;s
              rule list. Bundles can include the same rule across versions; the
              binding is idempotent.
            </p>
          </header>

          <div className="flex-1 overflow-y-auto px-5 py-4">
            {bundlesQ.isPending && (
              <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2 aria-hidden className="h-4 w-4 animate-spin" />
                Loading bundles…
              </div>
            )}

            {bundlesQ.isError && (
              <div
                role="alert"
                aria-live="assertive"
                className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning"
              >
                Couldn&rsquo;t load bundles. Try again or check the gateway is
                reachable.
              </div>
            )}

            {!bundlesQ.isPending &&
              !bundlesQ.isError &&
              (bundlesQ.data?.items?.length ?? 0) === 0 && (
                <EmptyState
                  title="No bundles yet"
                  description="Create a bundle in Bundle Studio first, then come back to publish this rule into it."
                  action={
                    <Button variant="ghost" disabled title="Bundle creation lives in Bundle Studio">
                      + New bundle (Bundle Studio)
                    </Button>
                  }
                />
              )}

            {!bundlesQ.isPending && (bundlesQ.data?.items?.length ?? 0) > 0 && (
              <fieldset className="flex flex-col gap-2" data-testid="publish-bundle-picker">
                <legend className="mb-1 text-xs uppercase tracking-wide text-muted-foreground">
                  Active bundles
                </legend>
                {bundlesQ.data!.items.map((bundle) => (
                  <label
                    key={bundle.id}
                    className={`flex cursor-pointer items-start gap-3 rounded-lg border px-3 py-2 text-sm transition-colors ${
                      selected === bundle.id
                        ? "border-cordum bg-cordum/5"
                        : "border-border hover:border-cordum/50"
                    }`}
                  >
                    <input
                      type="radio"
                      name="bundle"
                      value={bundle.id}
                      checked={selected === bundle.id}
                      onChange={() => setSelected(bundle.id)}
                      className="mt-0.5"
                    />
                    <span className="flex-1">
                      <span className="block font-medium text-foreground">
                        {bundle.name || bundle.id}
                      </span>
                      <span className="block text-xs text-muted-foreground">
                        Scope: {bundle.scope_binding.kind}
                        {bundle.scope_binding.value ? `:${bundle.scope_binding.value}` : ""}
                        {" · "}
                        {bundle.rule_ids?.length ?? 0} rules
                      </span>
                    </span>
                  </label>
                ))}
              </fieldset>
            )}

            {feedback.kind === "rule_not_found" && (
              <p
                role="alert"
                aria-live="assertive"
                className="mt-3 rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning"
              >
                The rule <code>{feedback.ruleId}</code> isn&rsquo;t saved on the
                server yet. Click <strong>Save draft</strong> first, then try
                again.
              </p>
            )}
            {feedback.kind === "bundle_not_found" && (
              <p
                role="alert"
                aria-live="assertive"
                className="mt-3 rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning"
              >
                The bundle <code>{feedback.bundleId}</code> was deleted while
                this dialog was open. Pick a different bundle or close and
                refresh.
              </p>
            )}
            {feedback.kind === "error" && (
              <p
                role="alert"
                aria-live="assertive"
                className="mt-3 rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning"
              >
                {feedback.message}
              </p>
            )}
          </div>

          <footer className="mt-auto flex items-center justify-end gap-2 border-t border-border px-5 py-3">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button
              type="submit"
              variant="primary"
              disabled={!selected || addRule.isPending}
              loading={addRule.isPending}
            >
              Publish to bundle…
            </Button>
          </footer>
        </form>
      </Drawer>

      <ConfirmDialog
        open={confirmOpen}
        title="Publish to bundle?"
        description={
          <>
            Add rule <code>{ruleId}</code> to bundle{" "}
            <code>{selected}</code>. This is idempotent — repeating with the
            same rule is a no-op.
          </>
        }
        confirmLabel="Publish"
        confirmVariant="primary"
        onConfirm={handleConfirm}
        onCancel={() => setConfirmOpen(false)}
        loading={addRule.isPending}
      />
    </>
  );
}
