import { useState, useEffect, useCallback, useMemo, useRef } from "react";
import { useParams, useNavigate } from "react-router-dom";
import YAML from "yaml";
import { ArrowLeft, AlertTriangle, Save, Upload } from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { BundleDetailTabs, type BundleTab } from "@/components/policy/bundles/BundleDetailTabs";
import { BundleYamlEditor } from "@/components/policy/bundles/BundleYamlEditor";
import { BundleVisualPreview } from "@/components/policy/bundles/BundleVisualPreview";
import { BundleDiffView } from "@/components/policy/bundles/BundleDiffView";
import { BundleSnapshotHistory } from "@/components/policy/bundles/BundleSnapshotHistory";
import { BundlePublishDialog } from "@/components/policy/bundles/BundlePublishDialog";
import { BundleRollbackDialog } from "@/components/policy/bundles/BundleRollbackDialog";
import { BundleShadowTab } from "@/components/policy/bundles/BundleShadowTab";
import { BundleSignatureSection } from "@/components/policy/bundles/BundleSignatureSection";
import { PromoteShadowDialog } from "@/components/policy/PromoteShadowDialog";
import { usePolicyBundle, useUpdatePolicyBundle, usePublishPolicy, useRollbackPolicy } from "@/hooks/usePolicies";
import { usePolicyAccess } from "@/hooks/usePolicyAccess";
import type { GlobalPolicyParseIssue } from "@/types/policy";

export const BUNDLE_DETAIL_TABS = ["yaml", "preview", "diff", "history", "shadow"] as const;

export function decodeBundleId(encoded: string): string {
  return decodeURIComponent(encoded).replaceAll("~", "/");
}

export function validateBundleYaml(yaml: string): GlobalPolicyParseIssue[] {
  if (!yaml.trim()) return [];
  try {
    const doc = YAML.parseDocument(yaml);
    return doc.errors.map((err) => ({
      path: "root",
      message: err.message,
      severity: "error" as const,
      line: err.pos?.[0] !== undefined ? err.pos[0] : undefined,
    }));
  } catch (err) {
    return [{
      path: "root",
      message: err instanceof Error ? err.message : "Invalid YAML",
      severity: "error" as const,
    }];
  }
}

export default function BundleDetailPage() {
  const { id: rawId } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const policyAccess = usePolicyAccess();
  const bundleId = rawId ? decodeBundleId(rawId) : "";
  const { data: bundle, isLoading, isError, error, refetch } = usePolicyBundle(bundleId);
  const updateBundle = useUpdatePolicyBundle();
  const publishPolicy = usePublishPolicy();
  const rollbackPolicy = useRollbackPolicy();

  // Default to preview for published bundles — YAML for drafts
  const urlTab = new URLSearchParams(window.location.search).get("tab") as BundleTab | null;
  const [activeTab, setActiveTab] = useState<BundleTab>(urlTab ?? "preview");
  const [yamlDraft, setYamlDraft] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [publishOpen, setPublishOpen] = useState(false);
  const [rollbackSnapshotId, setRollbackSnapshotId] = useState<string | null>(null);
  const [promoteOpen, setPromoteOpen] = useState(false);

  // Auto-switch to yaml tab for draft bundles (unless URL specifies a tab)
  useEffect(() => {
    if (bundle && !urlTab && bundle.status === "draft") {
      setActiveTab("yaml");
    }
  }, [bundle?.status, urlTab]);

  // Track server content to detect when draft matches after refetch
  const serverContent = bundle?.content ?? "";
  const currentYaml = yamlDraft ?? serverContent;
  const isDirty = yamlDraft !== null && yamlDraft !== serverContent;

  // Validate YAML on each draft change
  const parseIssues = useMemo(() => validateBundleYaml(currentYaml), [currentYaml]);
  const hasParseErrors = parseIssues.some((i) => i.severity === "error");

  // Unsaved-change guard: warn on browser navigation/close
  const isDirtyRef = useRef(isDirty);
  isDirtyRef.current = isDirty;

  useEffect(() => {
    const handler = (e: BeforeUnloadEvent) => {
      if (isDirtyRef.current) {
        e.preventDefault();
      }
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, []);

  const handleSave = useCallback(async () => {
    if (!bundleId || !yamlDraft) return;
    setSaveError(null);
    try {
      await updateBundle.mutateAsync({
        id: bundleId,
        content: yamlDraft,
      });
      setYamlDraft(null); // Reset draft — server data will refresh via invalidation
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : "Save failed");
    }
  }, [bundleId, yamlDraft, updateBundle]);

  const handleDiscard = useCallback(() => {
    setYamlDraft(null);
    setSaveError(null);
  }, []);

  // Publish: save draft first if dirty, then open confirmation, then publish
  const handlePublishRequest = useCallback(async () => {
    if (isDirty && yamlDraft && bundleId) {
      setSaveError(null);
      try {
        await updateBundle.mutateAsync({ id: bundleId, content: yamlDraft });
        setYamlDraft(null);
      } catch (err) {
        setSaveError(err instanceof Error ? err.message : "Save failed before publish");
        return;
      }
    }
    setPublishOpen(true);
  }, [isDirty, yamlDraft, bundleId, updateBundle]);

  const handlePublishConfirm = useCallback(async (note: string) => {
    if (!bundleId) return;
    try {
      await publishPolicy.mutateAsync({ bundleId, note: note || undefined });
      setPublishOpen(false);
    } catch {
      // Error toast handled by usePublishPolicy hook
    }
  }, [bundleId, publishPolicy]);

  const handleRollbackConfirm = useCallback(async () => {
    if (!rollbackSnapshotId) return;
    try {
      await rollbackPolicy.mutateAsync({ snapshotId: rollbackSnapshotId });
      setRollbackSnapshotId(null);
      setYamlDraft(null);
    } catch {
      // Error toast handled by useRollbackPolicy hook
    }
  }, [rollbackSnapshotId, rollbackPolicy]);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="sm" onClick={() => navigate("/govern/overview?tab=bundles")}>
            <ArrowLeft className="mr-1 h-3.5 w-3.5" />
            Bundles
          </Button>
          {isDirty && (
            <span className="text-xs font-mono text-[var(--color-warning)]">unsaved changes</span>
          )}
        </div>

        {bundle && (
          <div className="flex items-center gap-2">
            {policyAccess.canEdit && isDirty && (
              <Button variant="ghost" size="sm" onClick={handleDiscard}>
                Discard
              </Button>
            )}
            {policyAccess.canEdit && (
              <Button
                variant="outline"
                size="sm"
                disabled={!isDirty || hasParseErrors || updateBundle.isPending}
                onClick={() => void handleSave()}
              >
                <Save className="mr-1 h-3.5 w-3.5" />
                {updateBundle.isPending ? "Saving..." : "Save draft"}
              </Button>
            )}
            {policyAccess.canPublish && (
              <Button
                variant="outline"
                size="sm"
                disabled={hasParseErrors || publishPolicy.isPending || updateBundle.isPending}
                onClick={() => void handlePublishRequest()}
              >
                <Upload className="mr-1 h-3.5 w-3.5" />
                {publishPolicy.isPending ? "Publishing..." : "Publish"}
              </Button>
            )}
          </div>
        )}
      </div>

      {isLoading && (
        <div className="space-y-4">
          <SkeletonCard />
          <SkeletonCard />
        </div>
      )}

      {isError && (
        <EmptyState
          icon={<AlertTriangle className="w-6 h-6" />}
          title="Unable to load bundle"
          description={error instanceof Error ? error.message : "Failed to load policy bundle details."}
          action={
            <Button variant="outline" size="sm" onClick={() => void refetch()}>
              Retry
            </Button>
          }
        />
      )}

      {!isLoading && !isError && !bundle && (
        <EmptyState
          title="Bundle not found"
          description={`No policy bundle found with ID "${bundleId}".`}
        />
      )}

      {bundle && (
        <>
          <PageHeader
            label="Govern / Bundles"
            title={bundle.name || bundle.id}
            subtitle={`Bundle ${bundle.id}${bundle.version ? ` v${bundle.version}` : ""}`}
            actions={
              <div className="flex items-center gap-2">
                {bundle.enabled === false && (
                  <StatusBadge variant="muted">disabled</StatusBadge>
                )}
                <StatusBadge
                  variant={
                    (bundle.status ?? "").toLowerCase() === "published"
                      ? "healthy"
                      : (bundle.status ?? "").toLowerCase() === "draft"
                        ? "warning"
                        : "muted"
                  }
                >
                  {bundle.status ?? "unknown"}
                </StatusBadge>
                <StatusBadge variant={policyAccess.canPublish ? "healthy" : "muted"}>
                  {policyAccess.canPublish ? "publish access" : "read-only"}
                </StatusBadge>
              </div>
            }
          />

          <BundleSignatureSection bundle={bundle} />

          {policyAccess.isReadOnly && (
            <InfoBanner variant="warning">
              You have read-only access. YAML, diff, and snapshot history are visible but editing, publishing, and rollback are restricted.
            </InfoBanner>
          )}

          {saveError && (
            <InfoBanner variant="error" title="Save failed">
              {saveError}
            </InfoBanner>
          )}

          {parseIssues.length > 0 && (
            <InfoBanner
              variant={hasParseErrors ? "error" : "warning"}
              title={hasParseErrors ? "YAML validation errors" : "YAML validation warnings"}
            >
              <ul className="space-y-1">
                {parseIssues.map((issue, index) => (
                  <li key={`${issue.path}-${index}`}>
                    {issue.line ? `line ${issue.line} — ` : ""}
                    {issue.message}
                  </li>
                ))}
              </ul>
            </InfoBanner>
          )}

          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <div className="instrument-card p-4">
              <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider mb-2">rules</p>
              <p className="text-sm font-mono text-foreground">{bundle.rule_count ?? bundle.rules?.length ?? 0}</p>
            </div>
            <div className="instrument-card p-4">
              <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider mb-2">source</p>
              <p className="text-sm font-mono text-foreground truncate">{bundle.source ?? "—"}</p>
            </div>
            <div className="instrument-card p-4">
              <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider mb-2">author</p>
              <p className="text-sm font-mono text-foreground truncate">{bundle.author ?? "—"}</p>
            </div>
            <div className="instrument-card p-4">
              <p className="text-xs font-mono text-muted-foreground uppercase tracking-wider mb-2">sha256</p>
              <p className="text-sm font-mono text-foreground truncate">
                {bundle.sha256 ? `${bundle.sha256.slice(0, 16)}...` : "—"}
              </p>
            </div>
          </div>

          <BundleDetailTabs active={activeTab} onChange={setActiveTab} snapshotCount={bundle?.snapshots?.length ?? 0} />

          <div className="min-h-[200px]">
            {activeTab === "yaml" && (
              <BundleYamlEditor
                yaml={currentYaml}
                editable={policyAccess.canEdit}
                onChange={setYamlDraft}
              />
            )}
            {activeTab === "preview" && (
              <BundleVisualPreview yaml={currentYaml} onSwitchToCode={() => setActiveTab("yaml")} />
            )}
            {activeTab === "diff" && (
              <BundleDiffView bundleId={bundleId} draftYaml={currentYaml} />
            )}
            {activeTab === "history" && (
              <BundleSnapshotHistory
                canRollback={policyAccess.canPublish}
                onRollback={(snapshotId) => setRollbackSnapshotId(snapshotId)}
              />
            )}
            {activeTab === "shadow" && (
              <BundleShadowTab
                bundleID={bundleId}
                activeContent={serverContent}
                canEdit={policyAccess.canEdit}
                onOpenPromote={() => setPromoteOpen(true)}
              />
            )}
          </div>

          <BundlePublishDialog
            open={publishOpen}
            bundleName={bundle.name || bundle.id}
            loading={publishPolicy.isPending}
            onClose={() => setPublishOpen(false)}
            onConfirm={(note) => void handlePublishConfirm(note)}
          />

          <BundleRollbackDialog
            open={rollbackSnapshotId !== null}
            snapshotId={rollbackSnapshotId ?? ""}
            loading={rollbackPolicy.isPending}
            onClose={() => setRollbackSnapshotId(null)}
            onConfirm={() => void handleRollbackConfirm()}
          />

          <PromoteShadowDialog
            open={promoteOpen}
            bundleID={bundleId}
            bundleName={bundle.name || bundle.id}
            activeContent={serverContent}
            onClose={() => setPromoteOpen(false)}
          />
        </>
      )}
    </div>
  );
}
