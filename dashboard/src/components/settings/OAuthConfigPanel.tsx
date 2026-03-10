import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Eye, EyeOff, Loader } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Input } from "../ui/Input";
import { cn } from "../../lib/utils";
import { useAuthConfigAdmin, useSetConfig } from "../../hooks/useSettings";

// ---------------------------------------------------------------------------
// Provider definitions
// ---------------------------------------------------------------------------

type OAuthProvider = "google" | "github" | "azure";

interface ProviderDef {
  id: OAuthProvider;
  label: string;
  defaultScopes: string;
}

const PROVIDERS: ProviderDef[] = [
  { id: "google", label: "Google", defaultScopes: "openid email profile" },
  { id: "github", label: "GitHub", defaultScopes: "read:user user:email" },
  { id: "azure", label: "Azure AD", defaultScopes: "openid email profile User.Read" },
];

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

const oauthSchema = z.object({
  clientId: z.string().min(1, "Client ID is required"),
  clientSecret: z.string().min(1, "Client secret is required"),
  scopes: z.string().optional(),
});

type OAuthForm = z.infer<typeof oauthSchema>;

// ---------------------------------------------------------------------------
// OAuthConfigPanel
// ---------------------------------------------------------------------------

export function OAuthConfigPanel() {
  const { data: authConfig, isLoading } = useAuthConfigAdmin();
  const setConfig = useSetConfig();
  const [provider, setProvider] = useState<OAuthProvider>("google");
  const [showSecret, setShowSecret] = useState(false);

  const providerDef = PROVIDERS.find((p) => p.id === provider)!;
  const redirectUri = `${window.location.origin}/auth/callback`;

  const {
    register,
    handleSubmit,
    formState: { errors, isDirty },
  } = useForm<OAuthForm>({
    resolver: zodResolver(oauthSchema),
    defaultValues: {
      clientId: "",
      clientSecret: "",
      scopes: providerDef.defaultScopes,
    },
  });

  function onSubmit(data: OAuthForm) {
    setConfig.mutate({
      auth: {
        oidc_enabled: true,
        oauth_provider: provider,
        oauth_client_id: data.clientId,
        oauth_client_secret: data.clientSecret,
        oauth_scopes: data.scopes,
      },
    });
  }

  if (isLoading) {
    return (
      <Card className="animate-pulse">
        <div className="space-y-3">
          <div className="h-4 w-48 rounded bg-surface2" />
          <div className="h-10 rounded bg-surface2" />
          <div className="h-10 rounded bg-surface2" />
        </div>
      </Card>
    );
  }

  return (
    <Card>
      <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-ink">OAuth Configuration</h3>
          {authConfig?.oidc_enabled && <Badge variant="success">Enabled</Badge>}
        </div>

        {/* Provider selector */}
        <div>
          <label className="mb-2 block text-xs font-semibold text-muted-foreground">
            Provider
          </label>
          <div className="flex gap-2">
            {PROVIDERS.map((p) => (
              <button
                key={p.id}
                type="button"
                className={cn(
                  "flex-1 rounded-xl border px-4 py-3 text-center text-xs font-semibold transition-colors",
                  provider === p.id
                    ? "border-accent bg-accent/10 text-accent"
                    : "border-border text-muted-foreground hover:text-ink hover:border-ink/20",
                )}
                onClick={() => setProvider(p.id)}
              >
                {p.label}
              </button>
            ))}
          </div>
        </div>

        <div>
          <label className="mb-1 block text-xs font-semibold text-muted-foreground">
            Client ID
          </label>
          <Input
            placeholder={`${providerDef.label} client ID`}
            {...register("clientId")}
          />
          {errors.clientId && (
            <p className="mt-1 text-xs text-danger">{errors.clientId.message}</p>
          )}
        </div>

        <div>
          <label className="mb-1 block text-xs font-semibold text-muted-foreground">
            Client Secret
          </label>
          <div className="relative">
            <Input
              type={showSecret ? "text" : "password"}
              placeholder="Client secret"
              {...register("clientSecret")}
              className="pr-10"
            />
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-muted-foreground hover:text-ink"
              onClick={() => setShowSecret((v) => !v)}
            >
              {showSecret ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
            </button>
          </div>
          {errors.clientSecret && (
            <p className="mt-1 text-xs text-danger">{errors.clientSecret.message}</p>
          )}
        </div>

        <div>
          <label className="mb-1 block text-xs font-semibold text-muted-foreground">
            Redirect URI
          </label>
          <Input value={redirectUri} readOnly className="bg-surface2/50 font-mono text-xs" />
          <p className="mt-1 text-[10px] text-muted-foreground">
            Add this URI to your {providerDef.label} OAuth app settings.
          </p>
        </div>

        <div>
          <label className="mb-1 block text-xs font-semibold text-muted-foreground">
            Scopes
          </label>
          <Input
            placeholder="Space-separated scopes"
            {...register("scopes")}
          />
          <p className="mt-1 text-[10px] text-muted-foreground">
            Defaults: {providerDef.defaultScopes}
          </p>
        </div>

        <div className="flex justify-end gap-3 border-t border-border pt-3">
          <Button type="submit" size="sm" disabled={!isDirty || setConfig.isPending}>
            {setConfig.isPending ? (
              <><Loader className="mr-1.5 h-3 w-3 animate-spin" /> Saving...</>
            ) : (
              "Save OAuth Config"
            )}
          </Button>
        </div>
      </form>
    </Card>
  );
}
