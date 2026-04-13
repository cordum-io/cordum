import type { ReactNode } from "react";
import { Lock, ArrowUpRight } from "lucide-react";
import { useLicense } from "@/hooks/useLicense";
import type { LicenseEntitlements } from "@/api/types";
import { cn } from "@/lib/utils";

type EntitlementKey = keyof {
  [K in keyof LicenseEntitlements as LicenseEntitlements[K] extends boolean | undefined ? K : never]: true;
};

interface EntitlementGateProps {
  /** The entitlement key to check (e.g. 'saml', 'scim', 'rbac', 'siemExport') */
  entitlement: EntitlementKey | EntitlementKey[];
  /** Display label for the feature */
  label: string;
  /** Description shown when the feature is locked */
  description?: string;
  /** The gated content */
  children: ReactNode;
  /** Override the upgrade URL (default: cordum.io/pricing) */
  upgradeUrl?: string;
  /** If true, show a compact inline gate instead of full-page overlay */
  inline?: boolean;
}

function isEntitled(
  entitlements: LicenseEntitlements | undefined,
  keys: EntitlementKey | EntitlementKey[],
): boolean {
  if (!entitlements) return false;
  const checks = Array.isArray(keys) ? keys : [keys];
  // Any of the listed entitlements being true grants access
  return checks.some((key) => entitlements[key] === true);
}

export function EntitlementGate({
  entitlement,
  label,
  description,
  children,
  upgradeUrl = "https://cordum.io/pricing",
  inline = false,
}: EntitlementGateProps) {
  const { data, isLoading, isError } = useLicense();

  // Fail open on loading/error — don't block the UI while license loads
  if (isLoading || isError) {
    return <>{children}</>;
  }

  const entitled = isEntitled(data?.entitlements, entitlement);

  if (entitled) {
    return <>{children}</>;
  }

  if (inline) {
    return (
      <div className="rounded-2xl border border-border/60 bg-surface-1/50 p-4">
        <div className="flex items-center gap-3">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-muted/30">
            <Lock className="h-4 w-4 text-muted-foreground" />
          </div>
          <div className="min-w-0 flex-1">
            <p className="text-sm font-semibold text-foreground">{label}</p>
            {description && (
              <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>
            )}
          </div>
          <a
            href={upgradeUrl}
            target="_blank"
            rel="noreferrer"
            className="inline-flex shrink-0 items-center gap-1 rounded-full border border-cordum/25 px-3 py-1.5 text-xs font-medium text-cordum transition-colors hover:bg-cordum/10"
          >
            Upgrade
            <ArrowUpRight className="h-3 w-3" />
          </a>
        </div>
      </div>
    );
  }

  return (
    <div className="relative">
      {/* Blurred/muted background showing the locked content shape */}
      <div className="pointer-events-none select-none opacity-[0.15] blur-[1px]" aria-hidden>
        {children}
      </div>

      {/* Lock overlay */}
      <div className="absolute inset-0 flex items-center justify-center">
        <div className="w-full max-w-md rounded-3xl border border-border bg-[color:var(--surface-glass)] p-8 text-center shadow-soft backdrop-blur-xl">
          <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-2xl bg-muted/30">
            <Lock className="h-6 w-6 text-muted-foreground" />
          </div>

          <h3 className="text-base font-display font-semibold text-foreground">
            {label}
          </h3>

          <p className="mx-auto mt-2 max-w-sm text-sm leading-relaxed text-muted-foreground">
            {description || `This feature requires an upgraded plan. Upgrade to unlock ${label.toLowerCase()}.`}
          </p>

          <p className="mt-1 text-xs text-muted-foreground">
            Current plan: <span className="font-medium text-foreground capitalize">{data?.plan ?? "community"}</span>
          </p>

          <a
            href={upgradeUrl}
            target="_blank"
            rel="noreferrer"
            className={cn(
              "mt-5 inline-flex items-center gap-1.5 rounded-full px-5 py-2.5 text-sm font-semibold transition-colors",
              "bg-cordum text-white shadow-glow hover:bg-cordum/90",
            )}
          >
            View pricing
            <ArrowUpRight className="h-4 w-4" />
          </a>
        </div>
      </div>
    </div>
  );
}
