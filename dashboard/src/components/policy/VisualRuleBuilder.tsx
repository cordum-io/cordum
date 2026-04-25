import { useCallback, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { Plus, Loader, Upload, FileCode } from "lucide-react";
import { put, post, del } from "../../api/client";
import type { PolicyRule } from "../../api/types";
import { usePolicyBundle, useToggleRule, encodePolicyBundleId } from "../../hooks/usePolicies";
import { Button } from "../ui/Button";
import { RuleCard } from "./RuleCard";
import { RuleEditor, type RuleEditorSaveData } from "./RuleEditor";
import { PublishControls } from "./PublishControls";

// ---------------------------------------------------------------------------
// Conflict detection
// ---------------------------------------------------------------------------

function isSubset(a: string[], b: string[]): boolean {
  if (a.length === 0) return true;
  const setB = new Set(b);
  return a.every((x) => setB.has(x));
}

function detectConflicts(rules: PolicyRule[]): Map<string, string> {
  const warnings = new Map<string, string>();
  for (let i = 1; i < rules.length; i++) {
    const current = rules[i];
    const curCaps = (current.matchCriteria?.capabilities as string[] | undefined) ?? [];
    const curTags = (current.matchCriteria?.riskTags as string[] | undefined) ?? [];

    for (let j = 0; j < i; j++) {
      const higher = rules[j];
      const hiCaps = (higher.matchCriteria?.capabilities as string[] | undefined) ?? [];
      const hiTags = (higher.matchCriteria?.riskTags as string[] | undefined) ?? [];

      if (isSubset(curCaps, hiCaps) && isSubset(curTags, hiTags)) {
        warnings.set(
          current.id,
          `This rule may never fire — rule #${j + 1} matches first`,
        );
        break;
      }
    }
  }
  return warnings;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

interface VisualRuleBuilderProps {
  bundleId: string;
  onEditYaml?: () => void;
}

export function VisualRuleBuilder({ bundleId, onEditYaml }: VisualRuleBuilderProps) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toggleRule = useToggleRule();
  const { data: bundle, isLoading, error } = usePolicyBundle(bundleId);
  const [editingRuleId, setEditingRuleId] = useState<string | null>(null);
  const [addingNew, setAddingNew] = useState(false);
  const [showPublish, setShowPublish] = useState(false);
  const dragIndex = useRef<number | null>(null);
  const editorRef = useRef<HTMLDivElement | null>(null);

  const rules = bundle?.rules ?? [];
  const conflicts = detectConflicts(rules);

  // --- Mutations ---

  const saveRuleMutation = useMutation({
    mutationFn: (payload: { ruleId?: string; data: RuleEditorSaveData }) => {
      const safeId = encodePolicyBundleId(bundleId);
      if (payload.ruleId) {
        return put(`/policy/bundles/${safeId}/rules/${payload.ruleId}`, payload.data);
      }
      return post(`/policy/bundles/${safeId}/rules`, payload.data);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["policy-bundle", bundleId] });
      setEditingRuleId(null);
      setAddingNew(false);
    },
  });

  const deleteRuleMutation = useMutation({
    mutationFn: (ruleId: string) =>
      del(`/policy/bundles/${encodePolicyBundleId(bundleId)}/rules/${ruleId}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["policy-bundle", bundleId] });
    },
  });

  const reorderMutation = useMutation({
    mutationFn: (ruleIds: string[]) =>
      put(`/policy/bundles/${encodePolicyBundleId(bundleId)}/reorder`, { ruleIds }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["policy-bundle", bundleId] });
    },
  });

  // --- Toggle & Test ---

  const handleToggleEnabled = useCallback(
    (ruleId: string, enabled: boolean) => {
      toggleRule.mutate({ bundleId, ruleId, enabled });
    },
    [toggleRule, bundleId],
  );

  const handleTest = useCallback(
    (rule: PolicyRule) => {
      const caps = (rule.matchCriteria?.capabilities as string[] | undefined) ?? [];
      const tags = (rule.matchCriteria?.riskTags as string[] | undefined) ?? [];
      const params = new URLSearchParams();
      if (caps.length) params.set("caps", caps.join(","));
      if (tags.length) params.set("tags", tags.join(","));
      navigate(`/govern/overview?tab=simulator&${params.toString()}`);
    },
    [navigate],
  );

  // --- Drag-and-drop ---

  const onDragStart = useCallback((_e: React.DragEvent, index: number) => {
    dragIndex.current = index;
  }, []);

  const onDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
  }, []);

  const onDrop = useCallback(
    (_e: React.DragEvent, dropIndex: number) => {
      const from = dragIndex.current;
      if (from === null || from === dropIndex) return;
      const reordered = [...rules];
      const [moved] = reordered.splice(from, 1);
      reordered.splice(dropIndex, 0, moved);
      const ruleIds = reordered.map((r) => r.id);
      reorderMutation.mutate(ruleIds);
      dragIndex.current = null;
    },
    [rules, reorderMutation],
  );

  // --- Render ---

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-16 text-sm text-muted-foreground">
        <Loader className="mr-2 h-4 w-4 animate-spin" />
        Loading policy rules...
      </div>
    );
  }

  if (error) {
    return (
      <div className="py-16 text-center text-sm text-danger">
        Failed to load policy bundle.
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="font-display text-lg font-semibold text-ink">
            {bundle?.name ?? "Policy Rules"}
          </h3>
          <p className="text-xs text-muted-foreground">
            {rules.length} rule{rules.length !== 1 ? "s" : ""} — first match wins
          </p>
        </div>
        <div className="flex items-center gap-2">
          {onEditYaml && (
            <Button
              variant="outline"
              size="sm"
              type="button"
              onClick={onEditYaml}
            >
              <FileCode className="h-4 w-4" />
              Edit YAML
            </Button>
          )}
          <Button
            size="sm"
            type="button"
            onClick={() => {
              setAddingNew(true);
              setEditingRuleId(null);
              setTimeout(() => editorRef.current?.scrollIntoView({ behavior: "smooth", block: "start" }), 50);
            }}
          >
            <Plus className="h-4 w-4" />
            Create Rule
          </Button>
          <Button
            variant="outline"
            size="sm"
            type="button"
            onClick={() => setShowPublish((v) => !v)}
          >
            <Upload className="h-4 w-4" />
            Publish
          </Button>
        </div>
      </div>

      {/* Inline publish controls */}
      {showPublish && (
        <PublishControls bundleId={bundleId} ruleCount={rules.length} />
      )}

      {/* Rule list */}
      <div className="space-y-2">
        {rules.map((rule, i) =>
          editingRuleId === rule.id ? (
            <RuleEditor
              key={rule.id}
              rule={rule}
              onSave={(data) =>
                saveRuleMutation.mutate({ ruleId: rule.id, data })
              }
              onCancel={() => setEditingRuleId(null)}
            />
          ) : (
            <RuleCard
              key={rule.id}
              rule={rule}
              index={i}
              conflictWarning={conflicts.get(rule.id)}
              onEdit={() => {
                setEditingRuleId(rule.id);
                setAddingNew(false);
              }}
              onDelete={() => deleteRuleMutation.mutate(rule.id)}
              onToggleEnabled={handleToggleEnabled}
              onTest={handleTest}
              onDragStart={onDragStart}
              onDragOver={onDragOver}
              onDrop={onDrop}
            />
          ),
        )}
      </div>

      {/* Add new rule */}
      <div ref={editorRef}>
        {addingNew ? (
          <RuleEditor
            onSave={(data) => saveRuleMutation.mutate({ data })}
            onCancel={() => setAddingNew(false)}
          />
        ) : (
          <Button
            variant="outline"
            size="sm"
            type="button"
            onClick={() => {
              setAddingNew(true);
              setEditingRuleId(null);
            }}
          >
            <Plus className="h-4 w-4" />
            Add Rule
          </Button>
        )}
      </div>

      {rules.length === 0 && !addingNew && (
        <div className="rounded-2xl border border-dashed border-border px-6 py-12 text-center text-sm text-muted-foreground">
          No rules yet. Click &ldquo;Create Rule&rdquo; to create your first policy rule.
        </div>
      )}
    </div>
  );
}
