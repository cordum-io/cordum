import type { HTMLAttributes } from "react";
import { ShieldCheck, ShieldAlert, ShieldQuestion } from "lucide-react";
import { cn } from "../lib/utils";

// SignatureBadge states map directly to the policy-bundle signature field.
//
//   true     -> Bundle has a verified ed25519 signature. Green / shield-check.
//   false    -> Bundle has NO signature. Strict-mode kernels will reject it.
//               Amber / shield-alert — alarming but not error-red, because
//               the operator can remedy by signing rather than rolling back.
//   "unknown"-> Signature field was absent from the API response (for
//               example when a bundle was saved before task-fcd39725's
//               signing pipeline landed, or when the backend is serving a
//               cached response without the signature block). Muted / help
//               glyph — NOT alarming, just incomplete data.
//
// Accessibility: every state pairs an icon + aria-label so colour is never
// the only signal. role="status" is used so screen readers announce
// changes without the aggressive interrupt of role="alert".

export type SignatureState = boolean | "unknown";

export interface SignatureBadgeProps
  extends Omit<HTMLAttributes<HTMLSpanElement>, "role"> {
  signed?: SignatureState;
  publicKeyId?: string;
  signedAt?: string;
  signedBy?: string;
  size?: "sm" | "md";
}

interface VariantConfig {
  label: string;
  ariaLabel: string;
  tone: "success" | "warning" | "muted";
  Icon: typeof ShieldCheck;
}

const VARIANTS: Record<"signed" | "unsigned" | "unknown", VariantConfig> = {
  signed: {
    label: "Signed",
    ariaLabel: "Policy bundle is cryptographically signed",
    tone: "success",
    Icon: ShieldCheck,
  },
  unsigned: {
    label: "Unsigned",
    ariaLabel:
      "Policy bundle is NOT signed. Strict-mode kernels will reject it.",
    tone: "warning",
    Icon: ShieldAlert,
  },
  unknown: {
    label: "Unknown",
    ariaLabel: "Policy bundle signature status is unknown",
    tone: "muted",
    Icon: ShieldQuestion,
  },
};

const TONE_CLASSES: Record<VariantConfig["tone"], string> = {
  success: "bg-success/10 text-success border-success/25",
  warning: "bg-warning/10 text-warning border-warning/25",
  muted: "bg-muted text-muted-foreground border-border",
};

const SIZE_CLASSES: Record<NonNullable<SignatureBadgeProps["size"]>, string> = {
  sm: "gap-1 px-1.5 py-0.5 text-[11px]",
  md: "gap-1.5 px-2.5 py-1 text-xs",
};

const ICON_SIZE_CLASSES: Record<NonNullable<SignatureBadgeProps["size"]>, string> = {
  sm: "h-3 w-3",
  md: "h-3.5 w-3.5",
};

function resolveVariant(signed: SignatureState | undefined): VariantConfig {
  if (signed === true) return VARIANTS.signed;
  if (signed === false) return VARIANTS.unsigned;
  return VARIANTS.unknown;
}

function buildTooltip(
  variant: "signed" | "unsigned" | "unknown",
  publicKeyId?: string,
  signedAt?: string,
  signedBy?: string,
): string | undefined {
  if (variant !== "signed") return undefined;
  const parts: string[] = [];
  if (publicKeyId) parts.push(`Key: ${publicKeyId}`);
  if (signedBy) parts.push(`Signed by: ${signedBy}`);
  if (signedAt) {
    const d = new Date(signedAt);
    if (!Number.isNaN(d.getTime())) {
      parts.push(`Signed at: ${d.toLocaleString()} (${d.toISOString()})`);
    } else {
      parts.push(`Signed at: ${signedAt}`);
    }
  }
  return parts.length > 0 ? parts.join("\n") : undefined;
}

export function SignatureBadge({
  signed,
  publicKeyId,
  signedAt,
  signedBy,
  size = "md",
  className,
  ...rest
}: SignatureBadgeProps) {
  const variantKey =
    signed === true ? "signed" : signed === false ? "unsigned" : "unknown";
  const variant = resolveVariant(signed);
  const Icon = variant.Icon;
  const tooltip = buildTooltip(variantKey, publicKeyId, signedAt, signedBy);

  return (
    <span
      role="status"
      aria-label={variant.ariaLabel}
      data-signature-state={variantKey}
      title={tooltip}
      className={cn(
        "inline-flex items-center rounded-full border font-medium tracking-tight",
        "transition-colors duration-200",
        TONE_CLASSES[variant.tone],
        SIZE_CLASSES[size],
        className,
      )}
      {...rest}
    >
      <Icon className={cn(ICON_SIZE_CLASSES[size], "shrink-0")} aria-hidden="true" />
      <span>{variant.label}</span>
    </span>
  );
}
