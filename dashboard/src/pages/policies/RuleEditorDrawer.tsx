import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { AlertTriangle, FileCode2, FormInput, Loader2, Shield, X } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Drawer } from "@/components/ui/Drawer";
import { EmptyState } from "@/components/ui/EmptyState";
import { Tabs } from "@/components/ui/Tabs";
import { ruleTypeIcon, ruleTypeLabel } from "@/lib/policy-studio/rule-type";
import {
  NEW_RULE_ID,
  parseCreateNewType,
  ruleHasKnownType,
  useRule,
} from "@/hooks/useRule";
import { useSaveRuleDraft } from "@/hooks/useSaveRuleDraft";
import type { NormalizedRule } from "@/hooks/useRulesList";
import type { RuleType } from "@/api/generated/model/ruleType";
import { logger } from "@/lib/logger";
import { RULE_TEMPLATES, type RuleTemplate } from "@/lib/policy-studio/templates";
import { yamlToPartialRule } from "@/lib/policy-studio/editor/yaml";
import { RuleTemplatesGallery } from "./RuleTemplatesGallery";
import type { RuleMonacoEditorHandle } from "./RuleMonacoEditor";
import { RuleInlineSimulator } from "./RuleInlineSimulator";

// Lazy-load the publish modal — the bundle list + ConfirmDialog only
// matter when an author clicks Publish, so we don't ship them in the
// initial drawer chunk.
const PublishToBundleModal = lazy(() => import("./PublishToBundleModal"));

// Monaco bundles to ~600 KB raw (per `dist/stats.html`); we lazy-load it so
// the /policies route stays under the 400 KB initial-chunk budget. The cost
// is paid by users opening the editor — never by users browsing the table.
// Phase 3B mounts RuleFormView as a sibling under a Tabs toggle so the
// Form view doesn't have to bring Monaco's bytes along on first paint.
const RuleMonacoEditor = lazy(() => import("./RuleMonacoEditor"));
const RuleFormView = lazy(() =>
  import("./RuleFormView").then((mod) => ({ default: mod.RuleFormView })),
);

type EditorMode = "yaml" | "form";

interface RuleEditorDrawerControl {
  ruleId: string;
  createType: ReturnType<typeof parseCreateNewType>;
  // Optional template id — when set with `new=true`, the drawer pre-fills
  // Monaco from the template's YAML stub instead of starting from the
  // empty draft. Set by D4's empty-state gallery cards.
  templateId: string | null;
  // Optional bundle id — when set, the drawer's Save flow auto-binds the
  // saved rule to the named bundle (Phase 3E wires the actual bind once
  // Backend 5c lands; for now we forward the param through without
  // breaking the URL contract).
  bundleId: string | null;
}

const EDITOR_QUERY_KEYS = [
  "rule",
  "open",
  "type",
  "new",
  "template",
  "bundle",
] as const;

/**
 * Reads the URL search params controlling the editor drawer. The contract
 * accepts BOTH legacy `rule=<id>|new` (3A) AND alternate `new=true&type=`
 * (D4 empty-state gallery cards + future cross-links from Decisions /
 * Bundles surfaces). Closing the drawer clears ONLY editor-specific keys
 * so unrelated filters (status, scope, search) survive a click-through.
 */
function useEditorDrawerControl(): {
  control: RuleEditorDrawerControl | null;
  closeDrawer: () => void;
} {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();

  const ruleParam = params.get("rule");
  const open = params.get("open");
  const typeParam = params.get("type");
  const newFlag = params.get("new");
  const templateParam = params.get("template");
  const bundleParam = params.get("bundle");

  const closeDrawer = useCallback(() => {
    const next = new URLSearchParams(params);
    for (const key of EDITOR_QUERY_KEYS) {
      next.delete(key);
    }
    const search = next.toString();
    setParams(next, { replace: true });
    // Defensive: the empty-search case still leaves a `?` in some history
    // implementations. Normalize to no-query when nothing remains so the
    // PoliciesPage URL is the canonical /policies form on close.
    if (!search) {
      navigate("/policies", { replace: true });
    }
  }, [params, setParams, navigate]);

  if (open !== "editor") {
    return { control: null, closeDrawer };
  }
  // Two equivalent create-new entry points:
  //  1) ?rule=new&type=...        (3A's original contract)
  //  2) ?new=true&type=...        (D4 empty-state gallery + cross-link contract)
  // We canonicalise on the NEW_RULE_ID sentinel so downstream useRule
  // doesn't have to know about both forms.
  const ruleId = ruleParam ?? (newFlag === "true" ? NEW_RULE_ID : null);
  if (!ruleId) {
    return { control: null, closeDrawer };
  }
  return {
    control: {
      ruleId,
      createType: parseCreateNewType(typeParam),
      templateId: templateParam,
      bundleId: bundleParam,
    },
    closeDrawer,
  };
}

/**
 * Public entry mounted on PoliciesPage. Renders nothing when the URL does
 * not request the editor; renders a Drawer + tabs + the active editor mode
 * otherwise. Loading / not-found / unsupported-type / backend-error states
 * are handled here so child editors can assume a valid `rule` prop.
 */
export function RuleEditorDrawer() {
  const { control, closeDrawer } = useEditorDrawerControl();
  if (!control) return null;
  return <RuleEditorDrawerContent control={control} onClose={closeDrawer} />;
}

interface RuleEditorDrawerContentProps {
  control: RuleEditorDrawerControl;
  onClose: () => void;
}

/**
 * Looks up a template by id and merges its YAML stub onto the empty draft.
 * Returns the base draft unchanged when the id is unknown OR when the
 * template's YAML fails to parse — defensive fallbacks keep the editor
 * from crashing on stale URLs or malformed stubs. The caller is
 * responsible for ensuring the rule type matches the URL `type` param.
 */
function applyTemplateToDraft(
  base: NormalizedRule,
  templateId: string,
): NormalizedRule {
  const template: RuleTemplate | undefined = RULE_TEMPLATES.find(
    (t) => t.id === templateId,
  );
  if (!template) {
    logger.warn("policy-studio-editor", "unknown template id; falling back to empty draft", {
      templateId,
    });
    return base;
  }
  const parsed = yamlToPartialRule(template.yaml, base);
  if (parsed.error || !parsed.rule) {
    logger.warn("policy-studio-editor", "template YAML failed to parse; falling back to empty draft", {
      templateId,
      error: parsed.error,
    });
    return base;
  }
  // Force the rule type onto the template's declared type; if the URL
  // type param diverged from the template's ruleType (e.g. a copy/paste
  // error in a deep link), the template's intent wins because the YAML
  // body is keyed to that type.
  return { ...parsed.rule, type: template.ruleType };
}

function RuleEditorDrawerContent({ control, onClose }: RuleEditorDrawerContentProps) {
  const { ruleId, createType, templateId } = control;
  const isCreateNew = ruleId === NEW_RULE_ID;
  const query = useRule({ id: ruleId, createType });
  // Local working copy. The drawer is the source of truth for the in-flight
  // edit; Monaco reads+writes through onChange. Initialized lazily from the
  // loaded rule and reset when the rule id changes.
  const [draft, setDraft] = useState<NormalizedRule | null>(null);
  const lastLoadedId = useRef<string | null>(null);

  useEffect(() => {
    if (lastLoadedId.current === ruleId) return;
    if (query.data) {
      // Apply template pre-fill on the create-new path when a template id
      // is present and resolves to a known stub. Falls back to the empty
      // draft when the template id is missing or unrecognized so the
      // drawer never crashes on stale URLs.
      const seeded =
        isCreateNew && templateId
          ? applyTemplateToDraft(query.data, templateId)
          : query.data;
      setDraft(seeded);
      lastLoadedId.current = ruleId;
    }
  }, [query.data, ruleId, isCreateNew, templateId]);

  const handleClose = useCallback(() => {
    // Future enhancement: ConfirmDialog on dirty state. For now we close
    // immediately — the pre-merge plan accepts losing in-progress edits on
    // explicit close because a draft has not yet been saved server-side.
    onClose();
  }, [onClose]);

  return (
    <Drawer open onClose={handleClose} size="xl" label="Rule editor">
      <DrawerHeader
        ruleId={ruleId}
        isCreateNew={isCreateNew}
        rule={draft ?? query.data}
        onClose={handleClose}
      />

      {query.isPending && <DrawerLoading />}

      {!query.isPending && query.isError && (
        <DrawerErrorState onRetry={() => query.refetch()} onClose={handleClose} />
      )}

      {!query.isPending && !query.isError && !query.data && (
        <DrawerNotFound ruleId={ruleId} createType={createType} onClose={handleClose} />
      )}

      {!query.isPending && !query.isError && draft && (
        <DrawerEditorBody
          draft={draft}
          onDraftChange={setDraft}
          isCreateNew={isCreateNew}
          originalVersion={query.data?.version ?? ""}
          onReloadFromServer={async () => {
            const next = await query.refetch();
            if (next.data) {
              setDraft(next.data);
              lastLoadedId.current = ruleId;
            }
          }}
        />
      )}
    </Drawer>
  );
}

function DrawerHeader({
  ruleId,
  isCreateNew,
  rule,
  onClose,
}: {
  ruleId: string;
  isCreateNew: boolean;
  rule: NormalizedRule | null | undefined;
  onClose: () => void;
}) {
  const Icon = rule ? ruleTypeIcon(rule.type) : FileCode2;
  const typeLabel = rule ? ruleTypeLabel(rule.type) : "Rule";
  const title = isCreateNew
    ? rule?.type === undefined
      ? "New rule"
      : `New ${typeLabel.toLowerCase()} rule`
    : rule?.name || ruleId;

  return (
    <div className="flex items-start justify-between gap-3 pb-4">
      <div className="flex items-center gap-3">
        <span className="inline-flex h-9 w-9 items-center justify-center rounded-2xl bg-cordum/10 text-cordum">
          <Icon aria-hidden className="h-4 w-4" />
        </span>
        <div className="space-y-0.5">
          <p className="text-xs font-mono uppercase tracking-wider text-muted-foreground">
            {typeLabel}
          </p>
          <h2 className="text-lg font-semibold text-foreground">{title}</h2>
        </div>
      </div>
      <Button
        aria-label="Close rule editor"
        size="icon"
        variant="ghost"
        onClick={onClose}
      >
        <X aria-hidden className="h-4 w-4" />
      </Button>
    </div>
  );
}

function DrawerLoading() {
  return (
    <div className="flex h-full items-center justify-center">
      <div className="flex flex-col items-center gap-2 text-muted-foreground">
        <Loader2 aria-hidden className="h-5 w-5 animate-spin" />
        <p className="text-sm">Loading rule…</p>
      </div>
    </div>
  );
}

function DrawerErrorState({
  onRetry,
  onClose,
}: {
  onRetry: () => void;
  onClose: () => void;
}) {
  return (
    <EmptyState
      icon={<AlertTriangle className="h-5 w-5 text-warning" />}
      title="Couldn't load this rule"
      description="The rule endpoint returned an error. Try again or close and reopen from the rules list."
      action={
        <div className="flex items-center gap-2">
          <Button variant="secondary" onClick={onRetry}>
            Retry
          </Button>
          <Button variant="ghost" onClick={onClose}>
            Close
          </Button>
        </div>
      }
    />
  );
}

function DrawerNotFound({
  ruleId,
  createType,
  onClose,
}: {
  ruleId: string;
  createType: ReturnType<typeof parseCreateNewType>;
  onClose: () => void;
}) {
  // Two distinct cases land here: an existing rule id that the backend does
  // not recognize, OR the create-new path with an unsupported `type` query
  // param. Both render through the same card but with copy that points the
  // author at the right next action.
  const isCreateNew = ruleId === NEW_RULE_ID;
  return (
    <EmptyState
      icon={<Shield className="h-5 w-5 text-muted-foreground" />}
      title={isCreateNew ? "Pick a rule type to start" : "Rule not found"}
      description={
        isCreateNew
          ? createType
            ? "The selected rule type is not yet supported by this editor."
            : "Open this drawer with a `type=` query (input, output, velocity, edge) to start a new rule."
          : `Rule "${ruleId}" doesn't exist or has been removed.`
      }
      action={
        <Button variant="secondary" onClick={onClose}>
          Close
        </Button>
      }
    />
  );
}

function DrawerEditorBody({
  draft,
  onDraftChange,
  isCreateNew,
  originalVersion,
  onReloadFromServer,
}: {
  draft: NormalizedRule;
  onDraftChange: (rule: NormalizedRule) => void;
  isCreateNew: boolean;
  originalVersion: string;
  onReloadFromServer: () => Promise<void>;
}) {
  // Hooks must be called unconditionally (rules-of-hooks). Mode + ref
  // state live above the unknown-type guard.
  const [mode, setMode] = useState<EditorMode>("yaml");
  const editorRef = useRef<RuleMonacoEditorHandle>(null);
  const handleEditorError = useCallback((component: string, err: unknown) => {
    logger.warn("policy-studio-editor", `${component} mount failure`, { err });
  }, []);
  const onInsertTemplate = useCallback(
    (template: { yaml: string }) => {
      // Templates are full Rule envelopes — appending one to an existing
      // full envelope creates duplicate top-level keys (id/name/type/...)
      // that the YAML parser rejects with "Map keys must be unique".
      // The author intent on a template click is "load this template",
      // so we replace the document wholesale; Monaco's undo stack still
      // holds the prior state for Ctrl+Z (QA reopen #1 fix 2026-05-10).
      editorRef.current?.replaceDocument(template.yaml);
    },
    [],
  );

  // Defensive: the rule type sentinel UNKNOWN_RULE_TYPE means the row was
  // unrecognized by the normalizer. We refuse to mount Monaco or Form
  // against an unknown type because we have no schema to validate
  // against.
  if (!ruleHasKnownType(draft)) {
    return (
      <EmptyState
        icon={<Shield className="h-5 w-5 text-muted-foreground" />}
        title="Unknown rule type"
        description="This row's type wasn't recognized. The editor cannot mount without a known schema. Refer to the rules list legend for supported types."
      />
    );
  }

  const knownTypeDraft = draft as NormalizedRule & { type: RuleType };

  return (
    <div className="flex h-[calc(100%-3.5rem)] flex-col gap-3">
      <div className="flex items-center gap-3">
        <Tabs
          ariaLabel="Editor mode"
          variant="segmented"
          activeTab={mode}
          onChange={(next) => setMode(next as EditorMode)}
          tabs={[
            { id: "yaml", label: "YAML", icon: <FileCode2 aria-hidden className="h-3.5 w-3.5" /> },
            { id: "form", label: "Form", icon: <FormInput aria-hidden className="h-3.5 w-3.5" /> },
          ]}
        />
        <span className="ml-auto rounded-full bg-surface-2 px-2 py-0.5 text-[0.6rem] font-mono tracking-wide text-muted-foreground">
          {mode === "yaml" ? "Schema-aware YAML" : "Structured fields"}
        </span>
      </div>

      {mode === "yaml" && <RuleTemplatesGallery onInsert={onInsertTemplate} />}

      <div className="flex-1 min-h-0 overflow-hidden rounded-2xl border border-border bg-surface-1">
        {mode === "yaml" ? (
          <Suspense
            fallback={
              <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                <Loader2 aria-hidden className="mr-2 h-4 w-4 animate-spin" /> Loading editor…
              </div>
            }
          >
            <RuleMonacoEditor
              ref={editorRef}
              rule={knownTypeDraft}
              onChange={onDraftChange}
              onError={(err) => handleEditorError("RuleMonacoEditor", err)}
            />
          </Suspense>
        ) : (
          <Suspense
            fallback={
              <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                <Loader2 aria-hidden className="mr-2 h-4 w-4 animate-spin" /> Loading form…
              </div>
            }
          >
            <div className="h-full overflow-y-auto p-3">
              <RuleFormView rule={knownTypeDraft} onChange={onDraftChange} />
            </div>
          </Suspense>
        )}
      </div>

      <RuleInlineSimulator draft={knownTypeDraft} />

      <DrawerActions
        draft={draft}
        isCreateNew={isCreateNew}
        originalVersion={originalVersion}
        onReloadFromServer={onReloadFromServer}
      />
    </div>
  );
}

function DrawerActions({
  draft,
  isCreateNew,
  originalVersion,
  onReloadFromServer,
}: {
  draft: NormalizedRule;
  isCreateNew: boolean;
  // Original loaded version, used as `If-Match` on update. Empty for the
  // create-new path. The drawer captures this from `useRule.data.version`
  // — never the in-form `draft.version`, which is server-managed and may
  // be stale relative to what the user clicked Save against.
  originalVersion: string;
  // Reload action wired by the parent drawer — refetches the canonical
  // server state and replaces the in-form draft. Used by the
  // stale-version banner so the user can recover from a 409 without
  // closing + reopening the drawer (which would lose any non-conflicting
  // edits to other unrelated rules).
  onReloadFromServer: () => Promise<void>;
}) {
  const save = useSaveRuleDraft();
  const [publishOpen, setPublishOpen] = useState(false);
  const [feedback, setFeedback] = useState<
    | { kind: "idle" }
    | { kind: "success"; message: string }
    | { kind: "error"; message: string }
    | {
        kind: "stale";
        currentVersion: string;
        currentAuditHash: string;
      }
  >({ kind: "idle" });

  const onSaveDraft = useCallback(async () => {
    setFeedback({ kind: "idle" });
    const result =
      isCreateNew || !originalVersion
        ? await save.mutateAsync({ mode: "create", rule: draft })
        : await save.mutateAsync({
            mode: "update",
            rule: draft,
            ifMatch: originalVersion,
          });
    if (result.ok) {
      setFeedback({
        kind: "success",
        message: `Saved (now at ${result.rule.version}).`,
      });
      return;
    }
    if (result.kind === "stale") {
      // D3E DoD #3 reload banner — show the server-current state and
      // refuse to overwrite. Step 5 wires a Reload action that pulls
      // the v(N+1) Rule into the form.
      setFeedback({
        kind: "stale",
        currentVersion: result.currentVersion,
        currentAuditHash: result.currentAuditHash,
      });
      return;
    }
    setFeedback({ kind: "error", message: result.error });
  }, [draft, isCreateNew, originalVersion, save]);

  return (
    <div className="flex flex-col gap-2 pt-1">
      {feedback.kind === "success" && (
        <div
          role="status"
          aria-live="polite"
          className="rounded-md border border-success/40 bg-success/10 px-3 py-1.5 text-xs text-success"
        >
          {feedback.message}
        </div>
      )}
      {feedback.kind === "error" && (
        <div
          role="alert"
          aria-live="assertive"
          className="rounded-md border border-warning/40 bg-warning/10 px-3 py-1.5 text-xs text-warning"
        >
          {feedback.message}
        </div>
      )}
      {feedback.kind === "stale" && (
        <div
          role="alert"
          aria-live="assertive"
          data-testid="rule-stale-version-banner"
          className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-warning bg-warning/10 px-3 py-2 text-xs text-warning"
        >
          <span>
            <strong className="font-semibold">Newer version on server.</strong>{" "}
            The rule was updated to <code>{feedback.currentVersion}</code> while
            you were editing. Reload to keep the latest changes; saving now
            would overwrite them.
          </span>
          <Button
            type="button"
            size="sm"
            variant="primary"
            data-testid="rule-stale-version-reload"
            onClick={async () => {
              await onReloadFromServer();
              setFeedback({ kind: "idle" });
            }}
          >
            Reload from server
          </Button>
        </div>
      )}
      <div className="flex items-center justify-end gap-2">
        <Button
          variant="primary"
          onClick={onSaveDraft}
          disabled={save.isPending}
          loading={save.isPending}
          title={
            isCreateNew
              ? "Create the rule"
              : "Save the in-progress rule as a draft"
          }
          aria-label={isCreateNew ? "Create rule" : "Save draft"}
        >
          {isCreateNew ? "Create rule" : "Save draft"}
        </Button>
        <Button
          variant="ghost"
          onClick={() => setPublishOpen(true)}
          disabled={!draft.id || isCreateNew}
          title={
            !draft.id || isCreateNew
              ? "Save the rule first, then publish it to a bundle"
              : "Add this rule to a bundle"
          }
          aria-label="Publish to bundle"
        >
          Publish to bundle…
        </Button>
      </div>
      {publishOpen && (
        <Suspense fallback={null}>
          <PublishToBundleModal
            ruleId={draft.id}
            open={publishOpen}
            onClose={() => setPublishOpen(false)}
          />
        </Suspense>
      )}
    </div>
  );
}

export default RuleEditorDrawer;
