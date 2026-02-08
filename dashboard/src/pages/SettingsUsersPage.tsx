import { useState } from "react";
import { ChevronDown, ChevronUp, Shield } from "lucide-react";
import { UsersTab } from "../components/settings/UsersTab";
import { SamlConfigPanel } from "../components/settings/SamlConfigPanel";
import { OAuthConfigPanel } from "../components/settings/OAuthConfigPanel";
import { SessionManagement } from "../components/settings/SessionManagement";
import { Card } from "../components/ui/Card";
import { Badge } from "../components/ui/Badge";
import { useAuthConfigAdmin } from "../hooks/useSettings";
import { cn } from "../lib/utils";

// ---------------------------------------------------------------------------
// CollapsibleSection
// ---------------------------------------------------------------------------

function CollapsibleSection({
  title,
  badge,
  defaultOpen = false,
  children,
}: {
  title: string;
  badge?: React.ReactNode;
  defaultOpen?: boolean;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);

  return (
    <div>
      <button
        type="button"
        className="flex w-full items-center justify-between rounded-xl border border-border bg-surface px-4 py-3 text-left transition-colors hover:bg-surface2/50"
        onClick={() => setOpen((v) => !v)}
      >
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold text-ink">{title}</h2>
          {badge}
        </div>
        {open ? (
          <ChevronUp className="h-4 w-4 text-muted" />
        ) : (
          <ChevronDown className="h-4 w-4 text-muted" />
        )}
      </button>

      <div
        className={cn(
          "overflow-hidden transition-all",
          open ? "mt-3 max-h-[2000px] opacity-100" : "max-h-0 opacity-0",
        )}
      >
        {children}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// SSO tab toggle
// ---------------------------------------------------------------------------

type SsoTab = "saml" | "oauth";

function SsoSection() {
  const { data: authConfig } = useAuthConfigAdmin();
  const [tab, setTab] = useState<SsoTab>("saml");

  const samlEnabled = !!authConfig?.saml_enabled;
  const oauthEnabled = !!authConfig?.oauth_enabled;
  const anyEnabled = samlEnabled || oauthEnabled;

  return (
    <div className="space-y-3">
      {!anyEnabled && (
        <Card>
          <div className="flex items-center gap-3 py-2">
            <Shield className="h-5 w-5 text-muted" />
            <div>
              <p className="text-sm font-medium text-ink">
                SSO is not configured
              </p>
              <p className="text-xs text-muted">
                Configure SAML 2.0 or OAuth below to enable single sign-on for
                your organization.
              </p>
            </div>
          </div>
        </Card>
      )}

      {/* Tab toggle */}
      <div className="flex gap-1 rounded-xl border border-border p-1">
        {(["saml", "oauth"] as const).map((t) => (
          <button
            key={t}
            type="button"
            className={cn(
              "flex-1 rounded-lg px-4 py-2 text-xs font-semibold transition-colors",
              tab === t
                ? "bg-accent/10 text-accent"
                : "text-muted hover:text-ink",
            )}
            onClick={() => setTab(t)}
          >
            {t === "saml" ? "SAML 2.0" : "OAuth"}
            {t === "saml" && samlEnabled && (
              <Badge variant="success" className="ml-2 scale-90">
                On
              </Badge>
            )}
            {t === "oauth" && oauthEnabled && (
              <Badge variant="success" className="ml-2 scale-90">
                On
              </Badge>
            )}
          </button>
        ))}
      </div>

      {tab === "saml" ? <SamlConfigPanel /> : <OAuthConfigPanel />}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

export default function SettingsUsersPage() {
  return (
    <div className="space-y-6">
      <UsersTab />

      <CollapsibleSection
        title="Single Sign-On (SSO)"
        badge={<Badge variant="info">Enterprise</Badge>}
      >
        <SsoSection />
      </CollapsibleSection>

      <CollapsibleSection title="Session Management">
        <SessionManagement />
      </CollapsibleSection>
    </div>
  );
}
