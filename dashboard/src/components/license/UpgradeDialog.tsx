import { Sparkles } from "lucide-react";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";

const DEFAULT_PRICING_URL = "https://cordum.io/pricing";

export interface UpgradeDialogProps {
  /** Whether the dialog is visible. */
  open: boolean;
  /** Called when the user dismisses the dialog (Cancel button, X, or escape). */
  onClose: () => void;
  /** Display label for the gated feature (e.g. "SSO & SAML"). */
  feature: string;
  /** Current plan label rendered in the body copy (e.g. "community"). */
  currentPlan?: string;
  /** Override the pricing URL — defaults to the public Cordum pricing page. */
  pricingUrl?: string;
}

/**
 * Modal that intercepts clicks on locked features. Wraps the shared
 * ConfirmDialog primitive so a11y, motion, and styling stay consistent.
 *
 * The "View pricing" CTA opens the pricing page in a new tab via window.open
 * (called from a user-gesture so popup blockers will allow it).
 */
export function UpgradeDialog({
  open,
  onClose,
  feature,
  currentPlan,
  pricingUrl = DEFAULT_PRICING_URL,
}: UpgradeDialogProps) {
  const planLabel = currentPlan?.trim() ? currentPlan : "your current plan";

  return (
    <ConfirmDialog
      open={open}
      onClose={onClose}
      onCancel={onClose}
      onConfirm={() => {
        if (typeof window !== "undefined") {
          window.open(pricingUrl, "_blank", "noopener,noreferrer");
        }
        onClose();
      }}
      title={`${feature} requires an upgraded plan`}
      description={`You're on ${planLabel}. Upgrade to unlock ${feature.toLowerCase()} and other enterprise features.`}
      confirmLabel="View pricing"
      cancelLabel="Maybe later"
      icon={Sparkles}
    />
  );
}
