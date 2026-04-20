import { useState } from "react";
import { motion } from "framer-motion";
import {
  Activity,
  ArrowUpRight,
  CheckCircle2,
  Cloud,
  Globe,
  Lock,
  Plus,
  Send,
  Server,
  Shield,
  Trash2,
  X,
  XCircle,
} from "lucide-react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post, del } from "@/api/client";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { DialogOverlay } from "@/components/ui/DialogOverlay";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Input } from "@/components/ui/Input";
import { LabeledField } from "@/components/ui/LabeledField";
import { SkeletonCard, SkeletonTable } from "@/components/ui/Skeleton";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Tabs } from "@/components/ui/Tabs";
import { UpgradePrompt } from "@/components/UpgradePrompt";
import { useLicense } from "@/hooks/useLicense";
import { cn } from "@/lib/utils";
import { toast } from "sonner";
import { friendlyError } from "@/lib/friendlyError";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface ExportConfig {
  type: string;
  entitled: boolean;
  webhook_url?: string;
  webhook_hmac_enabled?: boolean;
  syslog_addr?: string;
  dd_site?: string;
  dd_tags?: string;
  dd_api_key_set?: boolean;
  cw_log_group?: string;
  cw_log_stream?: string;
  cw_region?: string;
  retention?: string;
  retention_days?: number;
}

interface ExportHealth {
  backend: string;
  status: string;
  entitled: boolean;
}

interface LegalHold {
  id: string;
  tenant_id: string;
  reason: string;
  created_by: string;
  created_at: string;
  released_at?: string | null;
  released_by?: string;
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const BACKEND_INFO: Record<string, { label: string; icon: typeof Globe; color: string }> = {
  webhook: { label: "Webhook (HTTPS)", icon: Globe, color: "text-blue-400" },
  syslog: { label: "Syslog (RFC 5424)", icon: Server, color: "text-emerald-400" },
  datadog: { label: "Datadog Logs", icon: Activity, color: "text-purple-400" },
  cloudwatch: { label: "CloudWatch Logs", icon: Cloud, color: "text-amber-400" },
  none: { label: "Disabled", icon: XCircle, color: "text-muted-foreground" },
};

function ConfigRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-start justify-between gap-4 border-t border-border/70 py-3 first:border-t-0 first:pt-0 last:pb-0">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className={cn("max-w-[28rem] text-right text-sm text-foreground break-all", mono && "font-mono text-xs")}>
        {value}
      </dd>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main page
// ---------------------------------------------------------------------------

export default function SettingsAuditExportPage() {
  const queryClient = useQueryClient();
  const license = useLicense();
  const siemEntitled = license.data?.entitlements?.siemExport === true || license.data?.entitlements?.auditExport === true;
  const legalHoldEntitled = license.data?.entitlements?.legalHold === true;
  const [activeTab, setActiveTab] = useState("export");
  const [holdDialogOpen, setHoldDialogOpen] = useState(false);
  const [holdTenant, setHoldTenant] = useState("");
  const [holdReason, setHoldReason] = useState("");
  const [releaseTarget, setReleaseTarget] = useState<LegalHold | null>(null);

  // --- Export queries ---
  const { data: health, isLoading: healthLoading, isError: healthError, error: healthErr, refetch: refetchHealth } = useQuery<ExportHealth>({
    queryKey: ["audit", "export", "health"],
    queryFn: () => get<ExportHealth>("/audit/export/health"),
    enabled: siemEntitled,
    staleTime: 10_000,
    refetchInterval: 30_000,
  });

  const { data: config, isLoading: configLoading } = useQuery<ExportConfig>({
    queryKey: ["audit", "export", "config"],
    queryFn: () => get<ExportConfig>("/audit/export/config"),
    enabled: siemEntitled,
    staleTime: 30_000,
  });

  const testMutation = useMutation({
    mutationFn: () => post<{ success: boolean; message: string }>("/audit/export/test"),
    onSuccess: (data) => { toast.success("Test event sent", { description: data.message }); },
    onError: (err: Error) => { const f = friendlyError(err, "test audit export"); toast.error(f.title, { description: f.description }); },
  });

  // --- Legal hold queries ---
  const {
    data: holdsData,
    isLoading: holdsLoading,
    isError: holdsError,
    error: holdsErr,
    refetch: refetchHolds,
  } = useQuery<{ holds: LegalHold[] }>({
    queryKey: ["audit", "legal-holds"],
    queryFn: () => get<{ holds: LegalHold[] }>("/audit/legal-holds"),
    enabled: legalHoldEntitled,
    staleTime: 10_000,
  });

  const createHoldMutation = useMutation({
    mutationFn: () => post("/audit/legal-hold", { tenant_id: holdTenant || undefined, reason: holdReason }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["audit", "legal-holds"] });
      toast.success("Legal hold created");
      setHoldDialogOpen(false);
      setHoldTenant("");
      setHoldReason("");
    },
    onError: (err: Error) => { const f = friendlyError(err, "create legal hold"); toast.error(f.title, { description: f.description }); },
  });

  const releaseHoldMutation = useMutation({
    mutationFn: (id: string) => del(`/audit/legal-hold/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["audit", "legal-holds"] });
      toast.success("Legal hold released");
      setReleaseTarget(null);
    },
    onError: (err: Error) => { const f = friendlyError(err, "release legal hold"); toast.error(f.title, { description: f.description }); },
  });

  const holds = holdsData?.holds ?? [];
  const activeHolds = holds.filter(h => !h.released_at);
  const holdsLoadError = holdsError ? friendlyError(holdsErr, "load legal holds") : null;
  const isLoading = license.isLoading || healthLoading || configLoading;
  const backendType = config?.type ?? health?.backend ?? "none";
  const backendMeta = BACKEND_INFO[backendType] || BACKEND_INFO.none;
  const BackendIcon = backendMeta.icon;
  const isActive = health?.status === "active";
  const tabs = [
    { id: "export", label: "Export" },
    { id: "legal-hold", label: "Legal Hold", count: activeHolds.length },
  ];

  if (healthError && siemEntitled) {
    return <ErrorBanner message={healthErr instanceof Error ? healthErr.message : "Failed to load audit export status"} onRetry={() => void refetchHealth()} />;
  }

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader
        label="Settings"
        title="Audit & Compliance"
        subtitle="SIEM export configuration and legal hold management for audit data."
      />

      {/* Tabs */}
      <div className="flex items-center justify-between gap-4">
        <Tabs
          tabs={tabs}
          activeTab={activeTab}
          onChange={setActiveTab}
          variant="segmented"
          ariaLabel="Audit compliance tabs"
          className="w-full sm:w-auto"
        />
        {activeTab === "legal-hold" && legalHoldEntitled && (
          <Button variant="primary" size="sm" onClick={() => setHoldDialogOpen(true)}>
            <Plus className="w-3 h-3 mr-1" />Create Hold
          </Button>
        )}
      </div>

      {/* Export Tab */}
      {activeTab === "export" && (
        <>
          {!siemEntitled && (
            <UpgradePrompt label="SIEM Export" plan={license.data?.plan} forceVisible
              title="SIEM audit export requires Enterprise"
              description="Export audit events to webhook, syslog, Datadog, or CloudWatch. Available on Enterprise plans." />
          )}
          {isLoading && siemEntitled ? (
            <div className="grid gap-4 xl:grid-cols-2"><SkeletonCard /><SkeletonCard /></div>
          ) : siemEntitled && (
            <div className="grid gap-4 xl:grid-cols-2">
              {/* Backend status card */}
              <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.05 }} className="instrument-card">
                <div className="flex items-center justify-between mb-4">
                  <div className="flex items-center gap-2">
                    <BackendIcon className={cn("w-5 h-5", backendMeta.color)} />
                    <h3 className="text-sm font-display font-semibold text-foreground">{backendMeta.label}</h3>
                  </div>
                  <StatusBadge variant={isActive ? "healthy" : "danger"}>
                    {isActive ? "active" : backendType === "none" ? "disabled" : "inactive"}
                  </StatusBadge>
                </div>
                <dl className="space-y-0">
                  <ConfigRow label="Backend type" value={backendType} mono />
                  {config?.retention && <ConfigRow label="Retention" value={config.retention === "unlimited" ? "Unlimited" : `${config.retention_days} days`} />}
                </dl>
                <div className="mt-4 flex items-center gap-2">
                  <Button variant="primary" size="sm" onClick={() => testMutation.mutate()} loading={testMutation.isPending} disabled={!isActive}>
                    <Send className="w-3 h-3 mr-1" />Send Test Event
                  </Button>
                  {testMutation.isSuccess && (
                    <StatusBadge variant="healthy">
                      <CheckCircle2 className="w-3 h-3" />Sent
                    </StatusBadge>
                  )}
                </div>
              </motion.div>

              {/* Backend config card */}
              <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.1 }} className="instrument-card">
                <div className="flex items-center gap-2 mb-4">
                  <Shield className="w-5 h-5 text-cordum" />
                  <h3 className="text-sm font-display font-semibold text-foreground">Configuration</h3>
                </div>
                <dl className="space-y-0">
                  {backendType === "webhook" && (<><ConfigRow label="Endpoint URL" value={config?.webhook_url || "not set"} mono /><ConfigRow label="HMAC signing" value={config?.webhook_hmac_enabled ? "Enabled (SHA-256)" : "Disabled"} /></>)}
                  {backendType === "syslog" && <ConfigRow label="Syslog address" value={config?.syslog_addr || "not set"} mono />}
                  {backendType === "datadog" && (<><ConfigRow label="Datadog site" value={config?.dd_site || "us1 (default)"} /><ConfigRow label="Tags" value={config?.dd_tags || "none"} mono /><ConfigRow label="API key" value={config?.dd_api_key_set ? "configured" : "not set"} /></>)}
                  {backendType === "cloudwatch" && (<><ConfigRow label="Log group" value={config?.cw_log_group || "not set"} mono /><ConfigRow label="Log stream" value={config?.cw_log_stream || "not set"} mono /><ConfigRow label="AWS region" value={config?.cw_region || "not set"} /></>)}
                  {backendType === "none" && <p className="text-xs text-muted-foreground">No export backend configured. Set <code className="font-mono text-foreground">CORDUM_AUDIT_EXPORT_TYPE</code> to enable.</p>}
                </dl>
                {backendType !== "none" && (
                  <div className="mt-4 pt-3 border-t border-border">
                    <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest mb-1">Configuration source</p>
                    <p className="text-xs text-muted-foreground">
                      Backend configuration is set via environment variables. See the{" "}
                      <a href="https://docs.cordum.io/configuration#audit-export" target="_blank" rel="noreferrer" className="text-cordum hover:underline inline-flex items-center gap-0.5">
                        configuration reference <ArrowUpRight className="w-3 h-3" />
                      </a>
                    </p>
                  </div>
                )}
              </motion.div>
            </div>
          )}
        </>
      )}

      {/* Legal Hold Tab */}
      {activeTab === "legal-hold" && (
        <>
          {!legalHoldEntitled && (
            <UpgradePrompt label="Legal Hold" plan={license.data?.plan} forceVisible
              title="Legal hold requires Enterprise"
              description="Place immutable retention holds on tenant audit data for compliance and litigation. Available on Enterprise plans." />
          )}
          {legalHoldEntitled && (
            holdsLoading ? <SkeletonTable rows={3} /> :
            holdsLoadError ? (
              <ErrorBanner
                title={holdsLoadError.title}
                message={holdsLoadError.description}
                onRetry={() => void refetchHolds()}
              />
            ) :
            activeHolds.length === 0 ? (
              <EmptyState icon={<Lock className="w-8 h-8" />} title="No active legal holds" description="Audit data follows normal retention policy. Create a hold to prevent automatic deletion." />
            ) : (
              <div className="instrument-card overflow-hidden">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-border bg-surface-0">
                      <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Tenant</th>
                      <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Reason</th>
                      <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Created By</th>
                      <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Created</th>
                      <th className="text-right px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {activeHolds.map((hold) => (
                      <tr key={hold.id} className="border-b border-border last:border-0 hover:bg-surface-1 transition-colors">
                        <td className="px-5 py-3 font-mono text-xs text-foreground">{hold.tenant_id}</td>
                        <td className="px-5 py-3 text-sm text-foreground">{hold.reason}</td>
                        <td className="px-5 py-3 text-xs text-muted-foreground">{hold.created_by}</td>
                        <td className="px-5 py-3 text-xs text-muted-foreground">{new Date(hold.created_at).toLocaleDateString()}</td>
                        <td className="px-5 py-3 text-right">
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon"
                            className="h-8 w-8 text-destructive hover:bg-destructive/10"
                            onClick={() => setReleaseTarget(hold)}
                            title="Release hold"
                            aria-label={`Release legal hold ${hold.id}`}
                          >
                            <Trash2 className="w-3.5 h-3.5 text-destructive" />
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )
          )}
        </>
      )}

      {/* Create Hold Dialog */}
      <DialogOverlay
        open={holdDialogOpen}
        onClose={() => setHoldDialogOpen(false)}
        label="Create legal hold"
        className="w-[440px] rounded-3xl border border-border bg-surface-1 p-6 shadow-2xl"
      >
        <div className="mb-4 flex items-center justify-between gap-4">
          <div>
            <h2 className="text-sm font-display font-semibold text-foreground">Create Legal Hold</h2>
            <p className="mt-1 text-xs text-muted-foreground">
              Preserve audit records beyond normal retention for investigations or legal action.
            </p>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="h-8 w-8"
            onClick={() => setHoldDialogOpen(false)}
            aria-label="Close create legal hold dialog"
          >
            <X className="w-4 h-4 text-muted-foreground" />
          </Button>
        </div>
        <div className="space-y-4">
          <LabeledField
            label="Tenant (optional)"
            description="Leave empty to place the hold on the default tenant."
          >
            <Input
              type="text"
              value={holdTenant}
              onChange={(e) => setHoldTenant(e.target.value)}
              placeholder="default"
            />
          </LabeledField>
          <LabeledField
            label="Reason"
            description="Record the compliance or legal trigger for the retention hold."
          >
            <Input
              type="text"
              value={holdReason}
              onChange={(e) => setHoldReason(e.target.value)}
              placeholder="Litigation pending — case #12345"
            />
          </LabeledField>
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="ghost" size="sm" onClick={() => setHoldDialogOpen(false)}>Cancel</Button>
            <Button variant="primary" size="sm" onClick={() => createHoldMutation.mutate()} loading={createHoldMutation.isPending} disabled={!holdReason.trim()}>
              <Lock className="w-3 h-3 mr-1" />Create Hold
            </Button>
          </div>
        </div>
      </DialogOverlay>

      {/* Release Confirmation */}
      <ConfirmDialog
        open={!!releaseTarget}
        onClose={() => setReleaseTarget(null)}
        onConfirm={() => releaseTarget && releaseHoldMutation.mutate(releaseTarget.id)}
        title="Release Legal Hold"
        description={`Release the legal hold on tenant "${releaseTarget?.tenant_id}"? Audit data will resume normal retention policy. Previously retained data will NOT be deleted.`}
        confirmLabel="Release Hold"
        variant="destructive"
      />
    </motion.div>
  );
}
