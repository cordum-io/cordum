import { useMemo, useState } from "react";
import { motion } from "framer-motion";
import {
  ArrowUpRight,
  BadgeCheck,
  Copy,
  Eye,
  EyeOff,
  Key,
  RefreshCw,
  ShieldAlert,
  Users,
} from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/layout/PageHeader";
import { TierBadge, normalizeLicensePlan } from "@/components/TierBadge";
import { UpgradePrompt } from "@/components/UpgradePrompt";
import { Badge } from "@/components/ui/Badge";
import { Button } from "@/components/ui/Button";
import { DetailList } from "@/components/ui/DetailList";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { SkeletonCard, SkeletonTable } from "@/components/ui/Skeleton";
import { StatTile } from "@/components/ui/StatTile";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { useLicense } from "@/hooks/useLicense";
import { useRotateSCIMToken, useSCIMConfig, type SCIMProvisionedUser } from "@/hooks/useSCIMConfig";

function planLabel(plan?: string | null): string {
  const normalized = normalizeLicensePlan(plan);
  if (normalized === "enterprise") return "Enterprise";
  if (normalized === "team") return "Team";
  return "Community";
}

function openExternal(url: string) {
  window.open(url, "_blank", "noopener,noreferrer");
}

function formatSyncTime(raw?: string): string {
  if (!raw) return "Pending first sync";
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return raw;
  return parsed.toLocaleString();
}

async function copyToClipboard(value: string, label: string) {
  if (!value.trim() || typeof navigator === "undefined" || !navigator.clipboard) {
    toast.error(`Unable to copy ${label.toLowerCase()}`);
    return;
  }
  try {
    await navigator.clipboard.writeText(value);
    toast.success(`${label} copied`);
  } catch {
    toast.error(`Unable to copy ${label.toLowerCase()}`);
  }
}

function UserRow({ user }: { user: SCIMProvisionedUser }) {
  return (
    <div className="grid gap-3 px-4 py-4 md:grid-cols-[minmax(0,2.2fr)_auto_auto_minmax(0,1.2fr)] md:items-center">
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <p className="truncate text-sm font-semibold text-foreground">
            {user.displayName || user.userName}
          </p>
          <Badge variant="info" className="capitalize">
            {user.source || "scim"}
          </Badge>
        </div>
        <p className="mt-1 truncate text-xs text-muted-foreground">
          {user.email || user.userName}
        </p>
      </div>

      <div className="text-xs text-muted-foreground">
        <span className="block font-mono uppercase tracking-widest text-[10px] text-muted-foreground/80">
          Username
        </span>
        <span className="mt-1 block truncate text-foreground">{user.userName}</span>
      </div>

      <div>
        <Badge variant={user.active ? "success" : "warning"}>
          {user.active ? "Active" : "Disabled"}
        </Badge>
      </div>

      <div className="text-xs text-muted-foreground">
        <span className="block font-mono uppercase tracking-widest text-[10px] text-muted-foreground/80">
          Last sync
        </span>
        <span className="mt-1 block text-foreground">{formatSyncTime(user.syncedAt)}</span>
      </div>
    </div>
  );
}

export default function SettingsSCIMPage() {
  const license = useLicense();
  const entitled = Boolean(license.data?.entitlements.scim);
  const scim = useSCIMConfig(entitled);
  const rotateToken = useRotateSCIMToken();
  const [showSecret, setShowSecret] = useState(false);

  const plan = planLabel(license.data?.plan);
  const userCount = scim.data?.users.length ?? 0;
  const activeCount = useMemo(
    () => (scim.data?.users ?? []).filter((user) => user.active).length,
    [scim.data?.users],
  );

  if (license.isLoading && !license.data) {
    return (
      <div className="space-y-6">
        <PageHeader
          label="Settings"
          title="SCIM provisioning"
          subtitle="Publish the provisioning endpoint, rotate the IdP bearer token, and monitor SCIM-managed users."
        />
        <div className="grid gap-4 xl:grid-cols-2">
          <SkeletonCard />
          <SkeletonCard />
        </div>
        <SkeletonTable rows={4} />
      </div>
    );
  }

  if (license.isError && !license.data) {
    return (
      <ErrorBanner
        title="Unable to load SCIM entitlements"
        message={license.error instanceof Error ? license.error.message : "Failed to load license data"}
        onRetry={() => {
          void license.refetch();
        }}
      />
    );
  }

  if (!entitled) {
    return (
      <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
        <PageHeader
          label="Settings"
          title="SCIM provisioning"
          subtitle="Provisioning endpoints, bearer-token rotation, and SCIM-managed user visibility are reserved for licensed deployments."
          actions={
            <Button variant="outline" size="sm" onClick={() => openExternal("https://cordum.io/pricing")}>
              Compare tiers
              <ArrowUpRight className="ml-1 h-3.5 w-3.5" />
            </Button>
          }
        />

        <UpgradePrompt
          forceVisible
          label="SCIM provisioning"
          plan={plan}
          title={`SCIM provisioning is locked on ${plan}`}
          description="Upgrade to Team or Enterprise to publish SCIM 2.0 endpoints, issue provisioning tokens, and inspect synced users from the dashboard."
        />

        <section className="instrument-card space-y-5">
          <div className="flex items-start justify-between gap-4">
            <div>
              <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
                Locked controls
              </p>
              <h2 className="mt-1 text-lg font-display font-semibold text-foreground">
                Provisioning remains disabled on the active tier
              </h2>
            </div>
            <TierBadge plan={license.data?.plan} />
          </div>

          <p className="max-w-2xl text-sm leading-relaxed text-muted-foreground">
            Cordum will not expose SCIM discovery, user, or group provisioning routes until the
            SCIM entitlement is active. Upgrade the deployment, then reload this page to publish
            the endpoint URL and rotate a provisioning token for your identity provider team.
          </p>

          <div className="grid gap-4 md:grid-cols-2">
            {[
              {
                title: "What unlocks",
                lines: [
                  "SCIM discovery endpoints for Okta and Azure AD",
                  "Dedicated bearer-token management for provisioning",
                  "Dashboard visibility into SCIM-synced operators",
                ],
              },
              {
                title: "What stays blocked",
                lines: [
                  "User and group CRUD from external identity providers",
                  "Provisioning token rotation from the dashboard",
                  "Runtime monitoring of SCIM-managed accounts",
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

  if (scim.isLoading && !scim.data) {
    return (
      <div className="space-y-6">
        <PageHeader
          label="Settings"
          title="SCIM provisioning"
          subtitle="Publish the provisioning endpoint, rotate the IdP bearer token, and monitor SCIM-managed users."
        />
        <div className="grid gap-4 xl:grid-cols-2">
          <SkeletonCard />
          <SkeletonCard />
        </div>
        <SkeletonTable rows={4} />
      </div>
    );
  }

  if (scim.isError || !scim.data) {
    return (
      <ErrorBanner
        title="Unable to load SCIM settings"
        message={scim.error instanceof Error ? scim.error.message : "Failed to load SCIM settings"}
        onRetry={() => {
          void scim.refetch();
        }}
      />
    );
  }

  const tokenValue = showSecret
    ? scim.data.bearerToken?.trim() || "No bearer token configured"
    : scim.data.bearerTokenMasked?.trim() || "No bearer token configured";

  const handleRotate = () => {
    // Snapshot "was configured" at mutation-call time so the success toast
    // doesn't depend on stale `scim.data` when the onSuccess callback fires.
    // The query cache may have been invalidated, evicted, or the component
    // may be mid-unmount by the time onSuccess runs.
    const wasConfigured = Boolean(scim.data?.configured);
    rotateToken.mutate(undefined, {
      onSuccess: () => {
        setShowSecret(true);
        toast.success(wasConfigured ? "SCIM bearer token rotated" : "SCIM bearer token generated");
      },
      onError: (error) => {
        toast.error(error instanceof Error ? error.message : "Failed to rotate SCIM token");
      },
    });
  };

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader
        label="Settings"
        title="SCIM provisioning"
        subtitle="Give your identity provider team one endpoint and one bearer token, then watch synchronized users flow into the gateway."
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                void scim.refetch();
              }}
              loading={scim.isFetching}
            >
              Refresh
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => openExternal("https://cordum.io/docs")}
            >
              Open docs
              <ArrowUpRight className="ml-1 h-3.5 w-3.5" />
            </Button>
          </div>
        }
      />

      <div className="grid gap-4 xl:grid-cols-[1.2fr_0.8fr]">
        <motion.section
          initial={{ opacity: 0, y: 10 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.04 }}
          className="instrument-card space-y-5"
        >
          <div className="flex items-start justify-between gap-4">
            <div>
              <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
                Connection kit
              </p>
              <h2 className="mt-1 text-lg font-display font-semibold text-foreground">
                Provisioning endpoint and token
              </h2>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <TierBadge plan={license.data?.plan} />
              <StatusBadge variant={scim.data.configured ? "healthy" : "warning"}>
                {scim.data.configured ? <BadgeCheck className="h-3.5 w-3.5" /> : <ShieldAlert className="h-3.5 w-3.5" />}
                {scim.data.configured
                  ? scim.data.tokenManagedBy === "env"
                    ? "Env token active"
                    : "Provisioning ready"
                  : "Generate a bearer token"}
              </StatusBadge>
            </div>
          </div>

          <DetailList
            items={[
              {
                label: "Endpoint URL",
                value: scim.data.endpointUrl || "Unavailable",
                mono: true,
                align: "left",
                action: (
                  <Button
                    variant="outline"
                    size="sm"
                    aria-label="Copy endpoint URL"
                    onClick={() => void copyToClipboard(scim.data.endpointUrl || "", "Endpoint URL")}
                  >
                    <Copy className="h-3.5 w-3.5" />
                    Copy
                  </Button>
                ),
              },
              {
                label: "Bearer token",
                value: tokenValue,
                mono: true,
                align: "left",
                action: (
                  <div className="flex flex-wrap items-center gap-2">
                    <Button
                      variant="outline"
                      size="sm"
                      aria-label={showSecret ? "Hide bearer token" : "Reveal bearer token"}
                      onClick={() => setShowSecret((current) => !current)}
                    >
                      {showSecret ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
                      {showSecret ? "Hide" : "Reveal"}
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      aria-label="Copy bearer token"
                      onClick={() => void copyToClipboard(scim.data.bearerToken || "", "Bearer token")}
                      disabled={!scim.data.bearerToken}
                    >
                      <Copy className="h-3.5 w-3.5" />
                      Copy
                    </Button>
                  </div>
                ),
              },
              { label: "Token source", value: scim.data.tokenManagedBy || "none", align: "left" },
            ]}
          />

          <div className="flex flex-wrap items-center gap-3">
            <Button
              size="sm"
              loading={rotateToken.isPending}
              onClick={handleRotate}
            >
              {scim.data.configured ? <RefreshCw className="h-3.5 w-3.5" /> : <Key className="h-3.5 w-3.5" />}
              {scim.data.configured ? "Rotate token" : "Generate token"}
            </Button>
            <p className="text-xs text-muted-foreground">
              Share the endpoint URL and bearer token with your IdP owner. The token is isolated
              from your main API key.
            </p>
          </div>

          {scim.data.tokenManagedBy === "env" ? (
            <InfoBanner variant="warning" title="Environment token currently active">
              The gateway is serving the SCIM token from <code className="font-mono">CORDUM_SCIM_BEARER_TOKEN</code>.
              Rotating here will create a Redis-managed override for future requests.
            </InfoBanner>
          ) : !scim.data.configured ? (
            <InfoBanner variant="info" title="No provisioning token has been issued yet">
              Generate a bearer token, copy the endpoint URL, and paste both into your identity provider&apos;s SCIM app configuration.
            </InfoBanner>
          ) : (
            <InfoBanner variant="success" title="Provisioning runtime is ready">
              Discovery, user, and group routes are published. External systems can start syncing against the endpoint immediately.
            </InfoBanner>
          )}
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
                Runtime snapshot
              </p>
              <h2 className="mt-1 text-lg font-display font-semibold text-foreground">
                Provisioning activity
              </h2>
            </div>
            <Badge variant="enterprise">SCIM</Badge>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            {[
              {
                label: "Entitlement",
                value: "Active",
                tone: "healthy" as BadgeVariant,
                helperText: "SCIM is licensed for this deployment.",
              },
              {
                label: "Provisioning state",
                value: scim.data.configured ? "Ready" : "Waiting for token",
                tone: (scim.data.configured ? "healthy" : "warning") as BadgeVariant,
              },
              {
                label: "Users synced",
                value: userCount.toString(),
                tone: "info" as BadgeVariant,
              },
              {
                label: "Active users",
                value: activeCount.toString(),
                tone: (activeCount > 0 ? "healthy" : "muted") as BadgeVariant,
              },
            ].map((item) => (
              <StatTile
                key={item.label}
                label={item.label}
                value={item.value}
                accent={item.tone}
                helperText={item.helperText}
              />
            ))}
          </div>

          <InfoBanner variant="info" title="Synced user signal">
            Each SCIM-managed account shows its latest sync timestamp below. Disabled users remain visible so operators can audit deprovisioning events without leaving the dashboard.
          </InfoBanner>
        </motion.section>
      </div>

      <motion.section
        initial={{ opacity: 0, y: 10 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ delay: 0.12 }}
        className="instrument-card space-y-5"
      >
        <div className="flex items-start justify-between gap-4">
          <div>
            <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
              Synced directory
            </p>
            <h2 className="mt-1 text-lg font-display font-semibold text-foreground">
              SCIM-managed users
            </h2>
          </div>
          <Badge variant="info">{userCount} visible</Badge>
        </div>

        {userCount === 0 ? (
          <EmptyState
            icon={<Users className="w-8 h-8" />}
            title="No SCIM-managed users yet"
            description="Once your identity provider provisions users or groups into Cordum, they will appear here with their sync status and active state."
          />
        ) : (
          <div className="overflow-hidden rounded-3xl border border-border/80 bg-surface-1/40">
            <div className="hidden border-b border-border/80 px-4 py-3 text-[11px] font-mono uppercase tracking-widest text-muted-foreground md:grid md:grid-cols-[minmax(0,2.2fr)_auto_auto_minmax(0,1.2fr)]">
              <span>User</span>
              <span>Username</span>
              <span>Status</span>
              <span>Last sync</span>
            </div>
            <motion.div
              initial="hidden"
              animate="visible"
              variants={{
                visible: { transition: { staggerChildren: 0.05 } },
              }}
              className="divide-y divide-border/80"
            >
              {scim.data.users.map((user) => (
                <motion.div
                  key={user.id}
                  variants={{
                    hidden: { opacity: 0, y: 10 },
                    visible: { opacity: 1, y: 0 },
                  }}
                >
                  <UserRow user={user} />
                </motion.div>
              ))}
            </motion.div>
          </div>
        )}
      </motion.section>
    </motion.div>
  );
}
