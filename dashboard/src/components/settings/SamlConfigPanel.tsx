import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { CheckCircle, AlertTriangle, Loader } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Input } from "../ui/Input";
import { Textarea } from "../ui/Textarea";
import { useAuthConfigAdmin, useSetConfig } from "../../hooks/useSettings";

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

const samlSchema = z.object({
  metadataUrl: z.string().url("Must be a valid URL").or(z.literal("")),
  loginUrl: z.string().url("Must be a valid URL").or(z.literal("")),
  certificate: z.string().optional(),
});

type SamlForm = z.infer<typeof samlSchema>;

// ---------------------------------------------------------------------------
// SamlConfigPanel
// ---------------------------------------------------------------------------

export function SamlConfigPanel() {
  const { data: authConfig, isLoading } = useAuthConfigAdmin();
  const setConfig = useSetConfig();
  const [testResult, setTestResult] = useState<"success" | "error" | null>(null);
  const [testError, setTestError] = useState("");

  const {
    register,
    handleSubmit,
    getValues,
    formState: { errors, isDirty },
  } = useForm<SamlForm>({
    resolver: zodResolver(samlSchema),
    values: {
      metadataUrl: authConfig?.saml_metadata_url ?? "",
      loginUrl: authConfig?.saml_login_url ?? "",
      certificate: "",
    },
  });

  function onSubmit(data: SamlForm) {
    setConfig.mutate({
      auth: {
        saml_enabled: true,
        saml_metadata_url: data.metadataUrl || undefined,
        saml_login_url: data.loginUrl || undefined,
      },
    });
  }

  function handleTestConnection() {
    const url = getValues("metadataUrl");
    if (!url) {
      setTestResult("error");
      setTestError("Metadata URL is required for testing.");
      return;
    }
    try {
      new URL(url);
      setTestResult("success");
      setTestError("");
    } catch {
      setTestResult("error");
      setTestError("Invalid URL format.");
    }
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
          <h3 className="text-sm font-semibold text-ink">SAML 2.0 Configuration</h3>
          {authConfig?.saml_enabled && <Badge variant="success">Enabled</Badge>}
        </div>

        <div>
          <label className="mb-1 block text-xs font-semibold text-muted">
            Metadata URL
          </label>
          <Input
            placeholder="https://idp.example.com/metadata.xml"
            {...register("metadataUrl")}
          />
          {errors.metadataUrl && (
            <p className="mt-1 text-xs text-danger">{errors.metadataUrl.message}</p>
          )}
        </div>

        <div>
          <label className="mb-1 block text-xs font-semibold text-muted">
            Login URL
          </label>
          <Input
            placeholder="https://idp.example.com/sso/login"
            {...register("loginUrl")}
          />
          {errors.loginUrl && (
            <p className="mt-1 text-xs text-danger">{errors.loginUrl.message}</p>
          )}
        </div>

        <div>
          <label className="mb-1 block text-xs font-semibold text-muted">
            Entity ID
          </label>
          <Input
            value={authConfig?.default_tenant ?? "cordum-sp"}
            readOnly
            className="bg-surface2/50"
          />
          <p className="mt-1 text-[10px] text-muted">
            Use this as the Service Provider Entity ID in your IdP.
          </p>
        </div>

        <div>
          <label className="mb-1 block text-xs font-semibold text-muted">
            Certificate (PEM)
          </label>
          <Textarea
            rows={4}
            placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
            {...register("certificate")}
          />
        </div>

        {/* Test connection */}
        <div className="flex items-center gap-3">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={handleTestConnection}
          >
            Test Connection
          </Button>
          {testResult === "success" && (
            <span className="flex items-center gap-1 text-xs text-success">
              <CheckCircle className="h-3.5 w-3.5" /> URL format valid
            </span>
          )}
          {testResult === "error" && (
            <span className="flex items-center gap-1 text-xs text-danger">
              <AlertTriangle className="h-3.5 w-3.5" /> {testError}
            </span>
          )}
        </div>

        <div className="flex justify-end gap-3 border-t border-border pt-3">
          <Button type="submit" size="sm" disabled={!isDirty || setConfig.isPending}>
            {setConfig.isPending ? (
              <><Loader className="mr-1.5 h-3 w-3 animate-spin" /> Saving...</>
            ) : (
              "Save SAML Config"
            )}
          </Button>
        </div>
      </form>
    </Card>
  );
}
