import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { motion } from "framer-motion";
import { Save } from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { usePolicyStudioGlobal } from "@/hooks/usePolicyStudioGlobal";
import { usePolicyAccess } from "@/hooks/usePolicyAccess";
import { OutputPolicyControls } from "@/components/policy/output-rules/OutputPolicyControls";
import { OutputPolicyStatusBanner } from "@/components/policy/output-rules/OutputPolicyStatusBanner";
import { OutputRulesList } from "@/components/policy/output-rules/OutputRulesList";
import { OutputRuleEditorDrawer } from "@/components/policy/output-rules/OutputRuleEditorDrawer";

export const OUTPUT_RULES_PAGE_SECTIONS = [
  "output-policy-controls",
  "output-policy-status",
  "output-rules-list",
] as const;

export function getOutputRulesAffordances(canWriteOutputRules: boolean): {
  canSave: boolean;
  canAddRule: boolean;
  canEditRule: boolean;
  canDeleteRule: boolean;
  canReorderRule: boolean;
  canToggleRule: boolean;
  drawerReadOnly: boolean;
} {
  return {
    canSave: canWriteOutputRules,
    canAddRule: canWriteOutputRules,
    canEditRule: canWriteOutputRules,
    canDeleteRule: canWriteOutputRules,
    canReorderRule: canWriteOutputRules,
    canToggleRule: canWriteOutputRules,
    drawerReadOnly: !canWriteOutputRules,
  };
}

export default function OutputRulesPage({ hideHeader }: { hideHeader?: boolean } = {}) {
  const navigate = useNavigate();
  const policyAccess = usePolicyAccess();
  const canWriteOutputRules = policyAccess.canManageOutputRules;
  const affordances = getOutputRulesAffordances(canWriteOutputRules);
  const {
    bundles,
    selectedBundleId,
    setSelectedBundleId,
    policy,
    outputRules,
    setOutputPolicy,
    setOutputRules,
    toggleOutputRuleEnabled,
    reorderOutputRules,
    save,
    saveError,
    clearSaveError,
    isLoading,
    isDirty,
    isSaving,
  } = usePolicyStudioGlobal();
  const [editorOpen, setEditorOpen] = useState(false);
  const [editingIndex, setEditingIndex] = useState<number | null>(null);
  const editingRule = useMemo(
    () => (editingIndex === null ? null : outputRules[editingIndex] ?? null),
    [editingIndex, outputRules],
  );

  const saveChanges = async () => {
    const result = await save();
    if (result.ok) {
      toast.success("Output rules saved");
      clearSaveError();
      return;
    }
    toast.error(result.error?.message ?? "Failed to save output rules", {
      description: result.error?.details,
    });
  };

  if (isLoading && bundles.length === 0) {
    return (
      <div className="space-y-3">
        <SkeletonCard />
        <SkeletonCard />
      </div>
    );
  }

  if (!isLoading && bundles.length === 0) {
    return (
      <EmptyState
        title="No policy bundles found"
        description="Create a policy bundle to start configuring output rules."
        action={<Button variant="outline" size="sm" onClick={() => navigate("/govern/overview?tab=bundles")}>View bundles</Button>}
      />
    );
  }

  return (
    <motion.div
      className="space-y-6"
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3, ease: "easeOut" }}
    >
      {!hideHeader && (
        <PageHeader
          label="Govern"
          title="Output Rules"
          subtitle="Dedicated output-policy surface for scanner behavior, fail mode, and output rule enforcement."
          actions={
            <StatusBadge variant={canWriteOutputRules ? "healthy" : "muted"}>
              {canWriteOutputRules ? "editor access" : "read-only role"}
            </StatusBadge>
          }
        />
      )}

      <InfoBanner variant="cordum">
        Output Rules page is output-only: tune scanner fail mode, output policy activation, and output rule behavior without input-rule mixing.
      </InfoBanner>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <label className="text-xs text-muted-foreground">
          Bundle
          <select
            id="govern-output-rules-bundle-select"
            className="ml-2 h-8 rounded-2xl border border-border bg-surface-2 px-2 text-xs text-foreground"
            value={selectedBundleId}
            onChange={(event) => setSelectedBundleId(event.target.value)}
          >
            {bundles.map((bundle) => (
              <option key={bundle.id} value={bundle.id}>
                {bundle.name || bundle.id}
              </option>
            ))}
          </select>
        </label>
        {affordances.canSave && (
          <Button
            size="sm"
            disabled={isSaving || !selectedBundleId || !isDirty}
            onClick={() => void saveChanges()}
          >
            <Save className="mr-1 h-3.5 w-3.5" />
            {isSaving ? "Saving…" : "Save"}
          </Button>
        )}
      </div>

      {saveError && (
        <div className="rounded-2xl border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive" role="alert">
          <div className="mb-1 font-semibold">Output policy action failed</div>
          <p>{saveError.message}</p>
          {saveError.details && <p className="mt-1 text-destructive">{saveError.details}</p>}
        </div>
      )}

      <OutputPolicyStatusBanner
        outputPolicy={policy.outputPolicy}
        outputRules={outputRules}
      />
      {!canWriteOutputRules && (
        <div className="rounded-2xl border border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10 px-3 py-2 text-xs text-[var(--color-warning)]">
          Viewer mode: output policy controls are read-only. You can inspect rules and open read-only details.
        </div>
      )}

      <InstrumentCard accent="governance" className="p-5">
        <OutputPolicyControls
          outputPolicy={policy.outputPolicy}
          readOnly={affordances.drawerReadOnly}
          onChange={(nextOutputPolicy) => {
            if (!affordances.canEditRule) return;
            setOutputPolicy(nextOutputPolicy);
          }}
        />
      </InstrumentCard>

      <OutputRulesList
        rules={outputRules}
        canEdit={affordances.canEditRule}
        onAddRule={() => {
          setEditingIndex(null);
          setEditorOpen(true);
        }}
        onViewRule={(index) => {
          setEditingIndex(index);
          setEditorOpen(true);
        }}
        onEditRule={(index) => {
          setEditingIndex(index);
          setEditorOpen(true);
        }}
        onDeleteRule={(index) => {
          if (!affordances.canDeleteRule) return;
          setOutputRules((previous) =>
            previous.filter((_, ruleIndex) => ruleIndex !== index),
          );
        }}
        onToggleRule={(index) => {
          if (!affordances.canToggleRule) return;
          const result = toggleOutputRuleEnabled(index);
          if (!result.ok && result.error) {
            toast.error(result.error.message, { description: result.error.details });
          }
        }}
        onMoveRule={(from, to) => {
          if (!affordances.canReorderRule) return;
          const result = reorderOutputRules(from, to);
          if (!result.ok && result.error) {
            toast.error(result.error.message, { description: result.error.details });
          }
        }}
      />

      <OutputRuleEditorDrawer
        open={editorOpen}
        readOnly={affordances.drawerReadOnly}
        rule={editingRule}
        nextRuleIndex={outputRules.length + 1}
        onClose={() => {
          setEditorOpen(false);
          setEditingIndex(null);
        }}
        onSave={(nextRule) => {
          if (!affordances.canEditRule) return;
          setOutputRules((previous) =>
            editingIndex === null
              ? [...previous, nextRule]
              : previous.map((rule, ruleIndex) =>
                  ruleIndex === editingIndex ? nextRule : rule,
                ),
          );
          setEditorOpen(false);
          setEditingIndex(null);
        }}
      />
    </motion.div>
  );
}
