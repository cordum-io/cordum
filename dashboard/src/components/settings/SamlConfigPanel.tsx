import { useMemo, useState } from "react";
import {
  Building2,
  CheckCircle2,
  Copy,
  ExternalLink,
  Link2,
  ShieldAlert,
  ShieldCheck,
} from "lucide-react";
import { useSAMLConfig } from "@/hooks/useSAMLConfig";
import { cn } from "@/lib/utils";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Input } from "../ui/Input";
import { Skeleton } from "../ui/Skeleton";

interface SamlConfigPanelProps {
  entitled: boolean;
  className?: string;
}

interface ReadOnlyFieldProps {
  id: string;
  label: string;
  value: string;
  helper: string;
  disabled?: boolean;
  copied?: boolean;
  onCopy: () => void;
}

function ReadOnlyField({
  id,
  label,
  value,
  helper,
  disabled = false,
  copied = false,
  onCopy,
}: ReadOnlyFieldProps) {
  const displayValue = value.trim() || "Not configured";

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-3">
        <label htmlFor={id} className="text-xs font-semibold uppercase tracking-[0.12em] text-muted-foreground">
          {label}
        </label>
        <Button
          variant="outline"
          size="sm"
          onClick={onCopy}
          disabled={disabled || !value.trim()}
          aria-label={`Copy ${label}`}
        >
          <Copy className="h-3.5 w-3.5" />
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
      <Input
        id={id}
        value={displayValue}
        readOnly
        disabled={disabled}
        className={cn(
          "font-mono text-xs",
          !value.trim() && "text-muted-foreground",
        )}
      />
      <p className="text-xs leading-relaxed text-muted-foreground">{helper}</p>
    </div>
  );
}

export function SamlConfigPanel({ entitled, className }: SamlConfigPanelProps) {
  const { data, isLoading, isError, error, refetch } = useSAMLConfig();
  const [copiedField, setCopiedField] = useState<string | null>(null);
  const [testFeedback, setTestFeedback] = useState<string>("");

  const fields = useMemo(
    () => [
      {
        id: "saml-metadata-url",
        label: "Metadata URL",
        value: data?.metadataUrl ?? "",
        helper: "Publish this service-provider metadata URL in your identity provider.",
      },
      {
        id: "saml-entity-id",
        label: "Entity ID",
        value: data?.entityId ?? "",
        helper: "Cordum defaults the entity ID to the metadata URL for simpler IdP setup.",
      },
      {
        id: "saml-acs-url",
        label: "ACS URL",
        value: data?.acsUrl ?? "",
        helper: "Assertion Consumer Service endpoint that receives the SAML response.",
      },
      {
        id: "saml-login-url",
        label: "Login URL",
        value: data?.loginUrl ?? "",
        helper: "Use this URL for SP-initiated SAML login and smoke tests.",
      },
    ],
    [data],
  );

  const handleCopy = async (fieldId: string, value: string) => {
    if (!value.trim() || typeof navigator === "undefined" || !navigator.clipboard) {
      return;
    }
    try {
      await navigator.clipboard.writeText(value);
      setCopiedField(fieldId);
      window.setTimeout(() => setCopiedField((current) => (current === fieldId ? null : current)), 1600);
    } catch {
      setTestFeedback("Clipboard access is not available in this browser context.");
    }
  };

  const handleTestLogin = () => {
    if (!entitled) {
      setTestFeedback("Upgrade your license tier to enable SAML login testing from the dashboard.");
      return;
    }

    const loginUrl = data?.loginUrl?.trim();
    if (!loginUrl) {
      setTestFeedback("SAML login is not configured on the gateway yet.");
      return;
    }

    setTestFeedback("Opened the SAML login flow in a new tab.");
    const popup = window.open(loginUrl, "_blank", "noopener,noreferrer");
    if (!popup) {
      window.location.assign(loginUrl);
    }
  };

  if (isLoading) {
    return (
      <Card className={cn("space-y-4", className)}>
        <div className="space-y-2">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-3 w-72" />
        </div>
        <div className="grid gap-4 md:grid-cols-2">
          {Array.from({ length: 4 }).map((_, index) => (
            <div key={index} className="space-y-2">
              <Skeleton className="h-3 w-24" />
              <Skeleton className="h-9 w-full rounded-2xl" />
              <Skeleton className="h-3 w-48" />
            </div>
          ))}
        </div>
      </Card>
    );
  }

  if (isError) {
    return (
      <Card className={cn("space-y-4", className)}>
        <div className="flex items-start gap-3">
          <ShieldAlert className="mt-0.5 h-5 w-5 text-destructive" />
          <div>
            <h3 className="text-sm font-semibold text-foreground">Unable to load SAML settings</h3>
            <p className="mt-1 text-sm text-muted-foreground">
              {error?.message ?? "The dashboard could not read /api/v1/auth/config."}
            </p>
          </div>
        </div>
        <Button variant="outline" size="sm" onClick={() => void refetch()}>
          Retry
        </Button>
      </Card>
    );
  }

  const statusLabel = !entitled
    ? "Upgrade required"
    : data?.enabled
      ? "Enabled"
      : data?.configured
        ? "Configured"
        : "Awaiting server config";

  return (
    <Card className={cn("space-y-5", className)}>
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-2">
          <div className="flex items-center gap-2">
            <Building2 className="h-4 w-4 text-cordum" />
            <h3 className="text-lg font-semibold text-foreground">SAML 2.0 service provider</h3>
          </div>
          <p className="max-w-2xl text-sm leading-relaxed text-muted-foreground">
            Cordum publishes these gateway endpoints for your identity provider. Runtime settings are managed on the server via environment variables or Helm values, so the dashboard stays read-only and safe for operators.
          </p>
        </div>

        <span
          className={cn(
            "inline-flex items-center rounded-full border px-3 py-1 text-xs font-semibold uppercase tracking-[0.12em]",
            !entitled
              ? "border-[var(--color-warning)]/25 bg-[var(--color-warning)]/10 text-[var(--color-warning)]"
              : data?.enabled
                ? "border-[var(--color-success)]/25 bg-[var(--color-success)]/10 text-[var(--color-success)]"
                : "border-border bg-surface-1 text-muted-foreground",
          )}
        >
          {statusLabel}
        </span>
      </div>

      <div className={cn("grid gap-4 md:grid-cols-2", !entitled && "opacity-75") }>
        {fields.map((field) => (
          <ReadOnlyField
            key={field.id}
            {...field}
            disabled={!entitled}
            copied={copiedField === field.id}
            onCopy={() => void handleCopy(field.id, field.value)}
          />
        ))}
      </div>

      <div className="grid gap-4 md:grid-cols-3">
        <div className="rounded-3xl border border-border bg-surface-1/70 p-4">
          <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">Session TTL</p>
          <p className="mt-2 text-lg font-semibold text-foreground">{data?.sessionTtl ?? "24h"}</p>
          <p className="mt-1 text-xs text-muted-foreground">Browser sessions follow the gateway auth TTL.</p>
        </div>

        <div className="rounded-3xl border border-border bg-surface-1/70 p-4">
          <div className="flex items-center gap-2 text-foreground">
            {entitled ? (
              <ShieldCheck className="h-4 w-4 text-[var(--color-success)]" />
            ) : (
              <ShieldAlert className="h-4 w-4 text-[var(--color-warning)]" />
            )}
            <p className="text-sm font-semibold">License state</p>
          </div>
          <p className="mt-2 text-sm text-foreground">{entitled ? "SAML is available on this tier." : "SAML is locked on the active tier."}</p>
          <p className="mt-1 text-xs text-muted-foreground">Both the SSO and SAML entitlements must be active before the gateway will process requests.</p>
        </div>

        <div className="rounded-3xl border border-border bg-surface-1/70 p-4">
          <div className="flex items-center gap-2 text-foreground">
            <Link2 className="h-4 w-4 text-cordum" />
            <p className="text-sm font-semibold">Next operator step</p>
          </div>
          <p className="mt-2 text-sm text-foreground">Set the gateway SAML env vars, then import the metadata URL into your identity provider.</p>
          <p className="mt-1 text-xs text-muted-foreground">Use the test action below only after the gateway is configured and licensed.</p>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-3 border-t border-border pt-4">
        <Button variant="outline" size="sm" onClick={handleTestLogin} disabled={!entitled || !data?.loginUrl}>
          <ExternalLink className="h-3.5 w-3.5" />
          Test SAML login
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={() => window.open("https://cordum.io/docs", "_blank", "noopener,noreferrer")}
        >
          <ExternalLink className="h-3.5 w-3.5" />
          View docs
        </Button>
        {testFeedback ? (
          <span className="text-xs text-muted-foreground">{testFeedback}</span>
        ) : entitled && data?.enabled ? (
          <span className="inline-flex items-center gap-1 text-xs text-[var(--color-success)]">
            <CheckCircle2 className="h-3.5 w-3.5" />
            Gateway endpoints are ready for IdP onboarding.
          </span>
        ) : null}
      </div>
    </Card>
  );
}
