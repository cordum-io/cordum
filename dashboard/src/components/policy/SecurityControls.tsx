import { useState, useCallback } from "react";
import { Lock, Unlock, ShieldCheck, ShieldOff } from "lucide-react";
import {
  POLICY_CONFIG_SUPPORTED,
  usePolicyConfig,
  useUpdatePolicyConfig,
  useActivateLockdown,
  useDeactivateLockdown,
} from "../../hooks/usePolicies";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Textarea } from "../ui/Textarea";
import { logger } from "../../lib/logger";
import { cn } from "../../lib/utils";

// ---------------------------------------------------------------------------
// Confirm dialog (shared)
// ---------------------------------------------------------------------------

function ConfirmDialog({
  title,
  message,
  confirmLabel,
  variant,
  isPending,
  disabled,
  children,
  onConfirm,
  onCancel,
}: {
  title: string;
  message: string;
  confirmLabel: string;
  variant: "primary" | "danger";
  isPending: boolean;
  disabled?: boolean;
  children?: React.ReactNode;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <Card className="relative z-10 w-full max-w-md">
        <div className="space-y-4">
          <h3 className="font-display text-lg font-semibold text-ink">{title}</h3>
          <p className="text-sm text-muted-foreground">{message}</p>
          {children}
          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={onCancel} disabled={isPending}>
              Cancel
            </Button>
            <Button variant={variant} size="sm" onClick={onConfirm} disabled={isPending || disabled}>
              {isPending ? "Processing\u2026" : confirmLabel}
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}

// ---------------------------------------------------------------------------
// SecurityControls
// ---------------------------------------------------------------------------

export function SecurityControls() {
  const { data: config } = usePolicyConfig();
  const updateConfig = useUpdatePolicyConfig();
  const activateLockdown = useActivateLockdown();
  const deactivateLockdown = useDeactivateLockdown();
  const policyConfigSupported = POLICY_CONFIG_SUPPORTED;

  if (!policyConfigSupported) {
    return (
      <Card>
        <div className="space-y-3">
          <h3 className="font-display text-base font-semibold text-ink">
            Security Controls
          </h3>
          <p className="text-sm text-muted-foreground">
            Policy controls (default stance and emergency lockdown) are not available in this deployment.
          </p>
        </div>
      </Card>
    );
  }

  const stance = config?.defaultStance ?? "allow";
  const isLockdown = config?.lockdown ?? false;

  // Stance toggle
  const [showStanceDialog, setShowStanceDialog] = useState(false);
  const newStance = stance === "allow" ? "deny" : "allow";

  const handleStanceConfirm = useCallback(() => {
    logger.info("security-controls", "Changing default stance", { from: stance, to: newStance });
    updateConfig.mutate(
      { defaultStance: newStance },
      { onSuccess: () => setShowStanceDialog(false) },
    );
  }, [updateConfig, newStance, stance]);

  // Lockdown activation
  const [showLockdownDialog, setShowLockdownDialog] = useState(false);
  const [lockdownReason, setLockdownReason] = useState("");

  const handleActivateLockdown = useCallback(() => {
    logger.warn("security-controls", "Activating lockdown", { reason: lockdownReason });
    activateLockdown.mutate(
      { reason: lockdownReason },
      {
        onSuccess: () => {
          setShowLockdownDialog(false);
          setLockdownReason("");
        },
      },
    );
  }, [activateLockdown, lockdownReason]);

  // Lockdown deactivation
  const [showLiftDialog, setShowLiftDialog] = useState(false);

  const handleDeactivateLockdown = useCallback(() => {
    logger.info("security-controls", "Lifting lockdown");
    deactivateLockdown.mutate(undefined, {
      onSuccess: () => setShowLiftDialog(false),
    });
  }, [deactivateLockdown]);

  return (
    <>
      <Card>
        <div className="space-y-5">
          <h3 className="font-display text-base font-semibold text-ink">
            Security Controls
          </h3>

          {/* Default Stance */}
          <div className="flex items-center justify-between">
            <div className="space-y-1">
              <div className="flex items-center gap-2">
                {stance === "allow" ? (
                  <ShieldCheck className="h-4 w-4 text-success" />
                ) : (
                  <ShieldOff className="h-4 w-4 text-danger" />
                )}
                <span className="text-sm font-semibold text-ink">Default Stance</span>
                <Badge variant={stance === "allow" ? "success" : "danger"}>
                  {stance === "allow" ? "Allow" : "Deny"}
                </Badge>
              </div>
              <p className="text-xs text-muted-foreground">
                {stance === "allow"
                  ? "Jobs not matching any rule are allowed by default."
                  : "Jobs not matching any rule are denied by default."}
              </p>
            </div>
            <button
              type="button"
              role="switch"
              aria-checked={stance === "deny"}
              aria-label="Toggle default stance"
              className={cn(
                "relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors",
                stance === "deny" ? "bg-danger" : "bg-success",
              )}
              onClick={() => setShowStanceDialog(true)}
            >
              <span
                className={cn(
                  "pointer-events-none inline-block h-5 w-5 rounded-full bg-card shadow transition-transform",
                  stance === "deny" ? "translate-x-5" : "translate-x-0",
                )}
              />
            </button>
          </div>

          {/* Divider */}
          <div className="border-t border-border" />

          {/* Emergency Lockdown */}
          <div className="flex items-center justify-between">
            <div className="space-y-1">
              <div className="flex items-center gap-2">
                <Lock className={cn("h-4 w-4", isLockdown ? "text-danger animate-pulse" : "text-muted-foreground")} />
                <span className="text-sm font-semibold text-ink">Emergency Lockdown</span>
                {isLockdown && <Badge variant="danger">ACTIVE</Badge>}
              </div>
              <p className="text-xs text-muted-foreground">
                {isLockdown
                  ? `Activated${config?.lockdownBy ? ` by ${config.lockdownBy}` : ""}${config?.lockdownAt ? ` at ${new Date(config.lockdownAt).toLocaleString()}` : ""}. All jobs are being denied.`
                  : "Immediately deny ALL jobs regardless of rules. Use only in emergencies."}
              </p>
            </div>
            {isLockdown ? (
              <Button variant="primary" size="sm" onClick={() => setShowLiftDialog(true)}>
                <Unlock className="h-3.5 w-3.5" />
                Lift Lockdown
              </Button>
            ) : (
              <Button variant="danger" size="sm" onClick={() => setShowLockdownDialog(true)}>
                <Lock className="h-3.5 w-3.5" />
                Lockdown
              </Button>
            )}
          </div>
        </div>
      </Card>

      {/* Stance change dialog */}
      {showStanceDialog && (
        <ConfirmDialog
          title="Change Default Policy Stance"
          message={
            newStance === "deny"
              ? "Changing to Default Deny will block all jobs that don't explicitly match an allow rule. Are you sure?"
              : "Changing to Default Allow will permit jobs that don't match any rule. Are you sure?"
          }
          confirmLabel={newStance === "deny" ? "Switch to Deny" : "Switch to Allow"}
          variant={newStance === "deny" ? "danger" : "primary"}
          isPending={updateConfig.isPending}
          onConfirm={handleStanceConfirm}
          onCancel={() => setShowStanceDialog(false)}
        />
      )}

      {/* Activate lockdown dialog */}
      {showLockdownDialog && (
        <ConfirmDialog
          title="Activate Emergency Lockdown"
          message="This will immediately deny ALL jobs regardless of rules. Use only in emergencies."
          confirmLabel="Activate Lockdown"
          variant="danger"
          isPending={activateLockdown.isPending}
          disabled={lockdownReason.trim().length < 10}
          onConfirm={handleActivateLockdown}
          onCancel={() => { setShowLockdownDialog(false); setLockdownReason(""); }}
        >
          <div>
            <label htmlFor="lockdown-reason" className="mb-1 block text-xs font-semibold text-muted-foreground">
              Reason (required, min 10 characters)
            </label>
            <Textarea
              id="lockdown-reason"
              rows={2}
              value={lockdownReason}
              onChange={(e) => setLockdownReason(e.target.value)}
              placeholder="Describe the emergency..."
            />
            {lockdownReason.length > 0 && lockdownReason.trim().length < 10 && (
              <p className="mt-1 text-xs text-danger">At least 10 characters required.</p>
            )}
          </div>
        </ConfirmDialog>
      )}

      {/* Lift lockdown dialog */}
      {showLiftDialog && (
        <ConfirmDialog
          title="Lift Emergency Lockdown"
          message="This will restore normal policy evaluation. Jobs will be evaluated against rules again."
          confirmLabel="Lift Lockdown"
          variant="primary"
          isPending={deactivateLockdown.isPending}
          onConfirm={handleDeactivateLockdown}
          onCancel={() => setShowLiftDialog(false)}
        />
      )}
    </>
  );
}
