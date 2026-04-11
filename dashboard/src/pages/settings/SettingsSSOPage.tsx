import { motion } from "framer-motion";
import {
  ArrowUpRight,
  BadgeCheck,
  Building2,
  Clock3,
  ShieldAlert,
} from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { TierBadge, normalizeLicensePlan } from "@/components/TierBadge";
import { UpgradePrompt } from "@/components/UpgradePrompt";
import { SamlConfigPanel } from "@/components/settings/SamlConfigPanel";
import { Button } from "@/components/ui/Button";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { useLicense } from "@/hooks/useLicense";
import { useSAMLConfig } from "@/hooks/useSAMLConfig";
import { cn } from "@/lib/utils";

function planLabel(plan?: string | null): string {
  const normalized = normalizeLicensePlan(plan);
  if (normalized === "enterprise") return "Enterprise";
  if (normalized === "team") return "Team";
  return "Community";
}

function openExternal(url: string) {
  window.open(url, "_blank", "noopener,noreferrer");
}

function DetailRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-start justify-between gap-4 border-t border-border/70 py-3 first:border-t-0 first:pt-0 last:pb-0">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd
        className={cn(
          "max-w-[28rem] text-right text-sm text-foreground break-all",
          mono && "font-mono text-xs",
        )}
      >
        {value}
      </dd>
    </div>
  );
}

function ProviderStatePill({ enabled }: { enabled: boolean }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full border px-3 py-1 text-xs font-medium",
        enabled
          ? "border-[var(--color-success)]/20 bg-[var(--color-success)]/10 text-[var(--color-success)]"
          : "border-[var(--color-warning)]/20 bg-[var(--color-warning)]/10 text-[var(--color-warning)]",
      )}
    >
      {enabled ? <BadgeCheck className="h-3.5 w-3.5" /> : <ShieldAlert className="h-3.5 w-3.5" />}
      {enabled ? "Runtime enabled" : "Needs gateway config"}
    </span>
  );
}

export default function SettingsSSOPage() {
  const license = useLicense();
  const saml = useSAMLConfig();

  if (license.isLoading && !license.data) {
    return (
      <div className="space-y-6">
        <PageHeader
          label="Settings"
          title="SSO providers"
          subtitle="Inspect the gateway-published SAML and OIDC sign-in settings for your deployment."
        />
        <div className="grid gap-4 xl:grid-cols-2">
          <SkeletonCard />
          <SkeletonCard />
        </div>
        <SkeletonCard />
      </div>
    );
  }

  if (license.isError && !license.data) {
    return (
      <ErrorBanner
        title="Unable to load SSO entitlements"
        message={license.error instanceof Error ? license.error.message : "Failed to load license data"}
        onRetry={() => {
          void license.refetch();
        }}
      />
    );
  }

  const plan = planLabel(license.data?.plan);
  const isEntitled = Boolean(license.data?.entitlements.sso);

  if (!isEntitled) {
    return (
      <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
        <PageHeader
          label="Settings"
          title="SSO providers"
          subtitle="Enterprise identity providers, metadata publishing, and operator handoff details live here once the deployment is licensed for SSO."
          actions={
            <Button
              variant="outline"
              size="sm"
              onClick={() => openExternal("https://cordum.io/pricing")}
            >
              Compare tiers
              <ArrowUpRight className="ml-1 h-3.5 w-3.5" />
            </Button>
          }
        />

        <UpgradePrompt
          forceVisible
          label="Single sign-on"
          plan={plan}
          title={`SSO providers are locked on ${plan}`}
          description="Upgrade to Team or Enterprise to publish SAML metadata, enable OIDC browser redirects, and review identity-provider connection settings from the dashboard."
        />

        <section className="instrument-card space-y-5">
          <div className="flex items-start justify-between gap-4">
            <div>
              <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
                Locked controls
              </p>
              <h2 className="mt-1 text-lg font-display font-semibold text-foreground">
                Enterprise identity setup is unavailable
              </h2>
            </div>
            <TierBadge plan={license.data?.plan} />
          </div>

          <p className="max-w-2xl text-sm leading-relaxed text-muted-foreground">
            Cordum is currently enforcing {plan} entitlements, so SAML metadata, OIDC redirects,
            and runtime SSO controls stay disabled. Install a Team or Enterprise license, then
            reload this page to unlock the provider configuration surfaces.
          </p>

          <div className="grid gap-4 md:grid-cols-2">
            {[
              {
                title: "What unlocks",
                lines: [
                  "Gateway metadata and ACS endpoints for SAML",
                  "OIDC login redirects and callback handling",
                  "Dashboard visibility for both provider connection details",
                ],
              },
              {
                title: "What stays disabled",
                lines: [
                  "Runtime SSO sign-in in the login screen",
                  "Published provider details for your IdP team",
                  "Admin connection testing and rollout guidance",
                ],
              },
            ].map((card, index) => (
              <motion.div
                key={card.title}
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.04 + index * 0.04 }}
                className="rounded-3xl border border-border bg-surface-1/70 p-5"
              >
                <h3 className="text-sm font-display font-semibold text-foreground">{card.title}</h3>
                <ul className="mt-4 space-y-2 text-sm text-muted-foreground">
                  {card.lines.map((line) => (
                    <li key={line} className="flex items-start gap-2">
                      <span className="mt-1 h-1.5 w-1.5 rounded-full bg-cordum/70" />
                      <span>{line}</span>
                    </li>
                  ))}
                </ul>
              </motion.div>
            ))}
          </div>
        </section>
      </motion.div>
    );
  }

  if (saml.isLoading && !saml.data) {
    return (
      <div className="space-y-6">
        <PageHeader
          label="Settings"
          title="SSO providers"
          subtitle="Inspect the gateway-published SAML and OIDC sign-in settings for your deployment."
        />
        <div className="grid gap-4 xl:grid-cols-2">
          <SkeletonCard />
          <SkeletonCard />
        </div>
        <SkeletonCard />
      </div>
    );
  }

  if (saml.isError || !saml.data) {
    return (
      <ErrorBanner
        title="Unable to load SSO runtime settings"
        message={saml.error instanceof Error ? saml.error.message : "Failed to load authentication config"}
        onRetry={() => {
          void saml.refetch();
        }}
      />
    );
  }

  const samlData = saml.data;
  const oidcData = samlData.oidc;
  const samlRuntimeEnabled = samlData.enabled;
  const oidcRuntimeEnabled = oidcData.enabled;
  const anyRuntimeEnabled = samlRuntimeEnabled || oidcRuntimeEnabled;

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader
        label="Settings"
        title="SSO providers"
        subtitle="Share the SAML metadata and OIDC client details with your identity team, then validate both runtimes before rollout."
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => openExternal("https://cordum.io/docs")}
            >
              Open docs
              <ArrowUpRight className="ml-1 h-3.5 w-3.5" />
            </Button>
            {samlRuntimeEnabled && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => openExternal(samlData.metadataUrl)}
              >
                Open metadata
                <ArrowUpRight className="ml-1 h-3.5 w-3.5" />
              </Button>
            )}
          </div>
        }
      />

      <div className="grid gap-4 xl:grid-cols-2">
        <motion.section
          initial={{ opacity: 0, y: 10 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.04 }}
          className="instrument-card space-y-5"
        >
          <div className="flex items-start justify-between gap-4">
            <div>
              <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
                SAML provider
              </p>
              <h2 className="mt-1 text-lg font-display font-semibold text-foreground">
                SAML operator handoff
              </h2>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <TierBadge plan={license.data?.plan} />
              <ProviderStatePill enabled={samlRuntimeEnabled} />
            </div>
          </div>

          <dl>
            <DetailRow label="Metadata URL" value={samlData.metadataUrl} mono />
            <DetailRow label="ACS URL" value={samlData.acsUrl} mono />
            <DetailRow label="Login URL" value={samlData.loginUrl} mono />
            <DetailRow label="Entity ID" value={samlData.entityId} mono />
            <DetailRow label="Session TTL" value={samlData.sessionTtl} />
          </dl>
        </motion.section>

        <motion.section
          initial={{ opacity: 0, y: 10 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.08 }}
          className="instrument-card space-y-5"
        >
          <div className="flex items-start justify-between gap-4">
            <div>
              <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
                OIDC browser provider
              </p>
              <h2 className="mt-1 text-lg font-display font-semibold text-foreground">
                OIDC operator handoff
              </h2>
            </div>
            <ProviderStatePill enabled={oidcRuntimeEnabled} />
          </div>

          <dl>
            <DetailRow label="Issuer URL" value={oidcData.issuer || "Not configured"} mono />
            <DetailRow label="Login URL" value={oidcData.loginUrl || "Not configured"} mono />
            <DetailRow label="Redirect URI" value={oidcData.redirectUri || "Not configured"} mono />
            <DetailRow label="Client ID" value={oidcData.clientId || "Not configured"} mono />
            <DetailRow
              label="Scopes"
              value={oidcData.scopes.length > 0 ? oidcData.scopes.join(" ") : "openid profile email"}
            />
            <DetailRow
              label="Client secret"
              value={oidcData.clientSecretMasked || "Not configured"}
              mono
            />
          </dl>
        </motion.section>
      </div>

      <motion.section
        initial={{ opacity: 0, y: 10 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ delay: 0.12 }}
        className="instrument-card space-y-5"
      >
        <div className="flex items-center gap-2">
          <Building2 className="h-4 w-4 text-cordum" />
          <h2 className="text-sm font-display font-semibold text-foreground">
            Provider runtime status
          </h2>
        </div>

        <div className="space-y-3 rounded-3xl border border-border bg-surface-1/70 p-4">
          <div className="flex items-center justify-between gap-3 text-sm">
            <span className="text-muted-foreground">License gate</span>
            <span className="font-medium text-foreground">SSO enabled</span>
          </div>
          <div className="flex items-center justify-between gap-3 text-sm">
            <span className="text-muted-foreground">SAML runtime</span>
            <span className="font-medium text-foreground">
              {samlRuntimeEnabled ? "Publishing endpoints" : "Waiting for CORDUM_SAML_* env vars"}
            </span>
          </div>
          <div className="flex items-center justify-between gap-3 text-sm">
            <span className="text-muted-foreground">OIDC runtime</span>
            <span className="font-medium text-foreground">
              {oidcRuntimeEnabled ? "Publishing login + callback" : "Waiting for CORDUM_OIDC_* env vars"}
            </span>
          </div>
          <div className="flex items-center justify-between gap-3 text-sm">
            <span className="text-muted-foreground">Cookie/session TTL</span>
            <span className="font-medium text-foreground">{samlData.sessionTtl}</span>
          </div>
        </div>

        <InfoBanner
          variant={anyRuntimeEnabled ? "success" : "warning"}
          title={anyRuntimeEnabled ? "Ready for provider testing" : "Configuration required"}
        >
          {anyRuntimeEnabled
            ? "Share the published provider details with your identity team, then use the test controls below to validate the browser redirect flows."
            : "The license is active, but the gateway is not yet publishing any SSO endpoints. Set the CORDUM_SAML_* or CORDUM_OIDC_* environment variables and restart the gateway."}
        </InfoBanner>

        <div className="grid gap-3 md:grid-cols-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => openExternal(samlData.loginUrl)}
            className="w-full"
            disabled={!samlRuntimeEnabled}
          >
            Test SAML login
            <ArrowUpRight className="ml-1 h-3.5 w-3.5" />
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => openExternal(oidcData.loginUrl)}
            className="w-full"
            disabled={!oidcRuntimeEnabled}
          >
            Test OIDC login
            <ArrowUpRight className="ml-1 h-3.5 w-3.5" />
          </Button>
        </div>
      </motion.section>

      <motion.section
        initial={{ opacity: 0, y: 10 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ delay: 0.16 }}
        className="space-y-4"
      >
        <div className="flex items-center gap-2">
          <Clock3 className="h-4 w-4 text-cordum" />
          <h2 className="text-sm font-display font-semibold text-foreground">
            Dashboard controls
          </h2>
        </div>
        <SamlConfigPanel entitled={isEntitled} />
      </motion.section>
    </motion.div>
  );
}
