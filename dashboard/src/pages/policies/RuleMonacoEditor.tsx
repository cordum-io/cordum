import {
  forwardRef,
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react";
import { Loader2 } from "lucide-react";
import type { NormalizedRule } from "@/hooks/useRulesList";
import { logger } from "@/lib/logger";
import { ruleToYaml, yamlToPartialRule } from "@/lib/policy-studio/editor/yaml";
import { getRuleSchema, type RuleSchema } from "@/lib/policy-studio/schemas";
import {
  hoverDocumentationFor,
  propertyCompletionsFromSchema,
  validateAgainstSchema,
  type SchemaDiagnostic,
} from "@/lib/policy-studio/editor/yaml-diagnostics";
import {
  useMonacoNamespace,
  type MonacoCompletionItem,
  type MonacoNamespace,
} from "./useMonacoNamespace";

const MonacoEditor = lazy(() => import("@monaco-editor/react"));

const POLICY_STUDIO_MARKER_OWNER = "policy-studio-rule";

// Surface schema diagnostics + parse errors as Monaco markers so they
// render in the gutter and the Problems panel when the editor is loaded.
// In tests (no monaco), this is a no-op; the inline banner below the
// editor still shows the first issue so assertions stay stable.
function updateMonacoMarkers(
  monaco: MonacoNamespace | null,
  schemaIssues: ReadonlyArray<SchemaDiagnostic>,
  options: { parseError: string | null },
): void {
  if (!monaco) return;
  const models = monaco.editor.getModels();
  if (models.length === 0) return;
  const markers = [];
  if (options.parseError) {
    markers.push({
      severity: monaco.MarkerSeverity.Error,
      message: options.parseError,
      startLineNumber: 1,
      startColumn: 1,
      endLineNumber: 1,
      endColumn: 1,
    });
  }
  for (const issue of schemaIssues) {
    markers.push({
      severity: monaco.MarkerSeverity.Warning,
      message: `${issue.path ? `${issue.path}: ` : ""}${issue.message}`,
      startLineNumber: 1,
      startColumn: 1,
      endLineNumber: 1,
      endColumn: 1,
    });
  }
  for (const model of models) {
    monaco.editor.setModelMarkers(model, POLICY_STUDIO_MARKER_OWNER, markers);
  }
}

export interface RuleMonacoEditorHandle {
  /**
   * Inserts text at the Monaco cursor when the editor is focused; appends
   * at end-of-document otherwise. When the editor instance hasn't mounted
   * yet (or under the test stub), updates the local YAML state and
   * synchronously runs the parser so the parent draft still updates.
   *
   * Use this for SNIPPET/PARTIAL-YAML insertions that augment the existing
   * document. Inserting a full-envelope YAML (id/name/type/scope/...) into
   * an existing document creates duplicate top-level keys and the YAML
   * parser rejects it — use `replaceDocument` for full-template loads.
   */
  insertText(text: string): void;
  /**
   * Replaces the entire editor document with the given YAML text. Used by
   * the templates gallery where a click means "load this template" — the
   * existing draft is swapped wholesale rather than appended (which would
   * collide on duplicate envelope keys for full-rule templates). The
   * replacement runs through the same parser → onChange path, so the
   * parent's canonical draft updates synchronously.
   */
  replaceDocument(text: string): void;
}

interface RuleMonacoEditorProps {
  rule: NormalizedRule;
  onChange: (rule: NormalizedRule) => void;
  onError?: (err: unknown) => void;
}

interface MinimalMonacoRange {
  startLineNumber: number;
  startColumn: number;
  endLineNumber: number;
  endColumn: number;
}

interface MinimalMonacoModel {
  getLineCount(): number;
  getLineMaxColumn(line: number): number;
  getValueLength(): number;
}

interface MinimalMonacoEditor {
  hasTextFocus(): boolean;
  getSelection(): MinimalMonacoRange | null;
  getModel(): MinimalMonacoModel | null;
  executeEdits(
    source: string,
    edits: Array<{
      range: MinimalMonacoRange;
      text: string;
      forceMoveMarkers?: boolean;
    }>,
  ): boolean;
}

// Debounce window for Monaco's onChange → parser → onChange-up. 300ms is
// the sweet spot: typists don't see flicker, schema validation runs on a
// quiet keyboard, and parse failures don't accumulate per keystroke.
const ONCHANGE_DEBOUNCE_MS = 300;

/**
 * Renders the YAML editor backed by Monaco. The component owns Monaco's
 * editor instance lifecycle and is the parser-of-record for YAML → Rule
 * conversion; it never writes parse failures back to the canonical Rule
 * because that would clobber a valid in-memory state with a transient
 * mid-keystroke parse error.
 */
const RuleMonacoEditor = forwardRef<RuleMonacoEditorHandle, RuleMonacoEditorProps>(
  function RuleMonacoEditor({ rule, onChange, onError }, ref) {
    // Initial YAML serialization happens once on mount and then again each
    // time the parent updates the rule (e.g. Form view edits, template
    // insertion). The lastSerializedFrom ref prevents a write-loop: after we
    // emit `onChange(rule)`, the parent re-renders with the same rule and
    // we'd otherwise re-serialize and overwrite the user's in-flight edits.
    const [yaml, setYaml] = useState<string>(() => ruleToYaml(rule));
    const lastEmittedRef = useRef<NormalizedRule | null>(rule);
    const debounceRef = useRef<number | undefined>(undefined);
    const editorRef = useRef<MinimalMonacoEditor | null>(null);
    const yamlRef = useRef<string>(yaml);
    const [parseError, setParseError] = useState<string | null>(null);
    const [schemaIssue, setSchemaIssue] = useState<string | null>(null);

    // Schema selection drives every schema-aware behavior: completion,
    // hover, gutter diagnostics. Returning null for the UNKNOWN_RULE_TYPE
    // sentinel (preserves task-15537d13's safe-fallback contract) means
    // the editor refuses to register schema providers — fail closed.
    const schema: RuleSchema | null = useMemo(() => getRuleSchema(rule.type), [rule.type]);
    const schemaRef = useRef<RuleSchema | null>(schema);
    useEffect(() => {
      schemaRef.current = schema;
    }, [schema]);

    const monaco = useMonacoNamespace();

    useEffect(() => {
      yamlRef.current = yaml;
    }, [yaml]);

    useEffect(() => {
      if (lastEmittedRef.current === rule) {
        // We just emitted this rule — Monaco already has the matching YAML.
        return;
      }
      const next = ruleToYaml(rule);
      setYaml(next);
      yamlRef.current = next;
      lastEmittedRef.current = rule;
    }, [rule]);

    useEffect(() => {
      return () => {
        if (debounceRef.current !== undefined) {
          window.clearTimeout(debounceRef.current);
        }
      };
    }, []);

    const parseAndPropagate = useCallback(
      (text: string) => {
        try {
          const parsed = yamlToPartialRule(text, rule);
          if (parsed.error) {
            setParseError(parsed.error);
            setSchemaIssue(null);
            updateMonacoMarkers(monaco, [], { parseError: parsed.error });
            return;
          }
          setParseError(null);
          if (parsed.rule) {
            lastEmittedRef.current = parsed.rule;
            onChange(parsed.rule);
            // Schema validation against the active rule type. Diagnostics
            // surface BOTH in Monaco's gutter (when monaco is loaded) and
            // as a banner footer so the test stub path still renders the
            // first issue.
            const activeSchema = schemaRef.current;
            const issues = activeSchema
              ? validateAgainstSchema(parsed.rule, activeSchema)
              : [];
            setSchemaIssue(
              issues.length === 0
                ? null
                : `${issues.length} schema issue${issues.length === 1 ? "" : "s"}: ${issues[0].path || "(root)"} — ${issues[0].message}`,
            );
            updateMonacoMarkers(monaco, issues, { parseError: null });
          }
        } catch (err) {
          setParseError(err instanceof Error ? err.message : "YAML parse failed");
          setSchemaIssue(null);
          onError?.(err);
          logger.warn("policy-studio-editor", "yaml parse failure", { err });
        }
      },
      [monaco, onChange, onError, rule],
    );

    const handleYamlChange = useCallback(
      (next: string | undefined) => {
        const text = next ?? "";
        setYaml(text);
        yamlRef.current = text;
        if (debounceRef.current !== undefined) {
          window.clearTimeout(debounceRef.current);
        }
        debounceRef.current = window.setTimeout(
          () => parseAndPropagate(text),
          ONCHANGE_DEBOUNCE_MS,
        );
      },
      [parseAndPropagate],
    );

    const handleEditorMount = useCallback((editor: unknown) => {
      // Monaco's IStandaloneCodeEditor is structurally compatible with
      // MinimalMonacoEditor. We type the param as `unknown` so we don't
      // pull the heavy monaco-editor type definitions into the bundle/test
      // surface, then narrow at use sites.
      editorRef.current = editor as MinimalMonacoEditor;
    }, []);

    // Register schema-aware YAML completion + hover providers once the
    // Monaco namespace is loaded. The active schema is read through
    // `schemaRef`, so a rule-type change updates suggestions without
    // re-registering (registering twice would surface duplicates).
    useEffect(() => {
      if (!monaco) return;
      const completionDisposer = monaco.languages.registerCompletionItemProvider("yaml", {
        triggerCharacters: [":", " ", "-"],
        provideCompletionItems(model, position) {
          const activeSchema = schemaRef.current;
          if (!activeSchema) return { suggestions: [] };
          const word = model.getWordAtPosition(position);
          const items = propertyCompletionsFromSchema(activeSchema);
          const suggestions: MonacoCompletionItem[] = items.map((item) => ({
            label: item.label,
            kind:
              item.kind === "value"
                ? monaco.languages.CompletionItemKind.EnumMember
                : monaco.languages.CompletionItemKind.Property,
            insertText: item.label,
            documentation: item.documentation,
            range: word
              ? {
                  startLineNumber: position.lineNumber,
                  startColumn: word.startColumn,
                  endLineNumber: position.lineNumber,
                  endColumn: word.endColumn,
                }
              : undefined,
          }));
          return { suggestions };
        },
      });
      const hoverDisposer = monaco.languages.registerHoverProvider("yaml", {
        provideHover(model, position) {
          const activeSchema = schemaRef.current;
          if (!activeSchema) return null;
          const word = model.getWordAtPosition(position);
          if (!word) return null;
          const documentation = hoverDocumentationFor(activeSchema, word.word);
          if (!documentation) return null;
          return { contents: [{ value: `**${word.word}** — ${documentation}` }] };
        },
      });
      return () => {
        completionDisposer.dispose();
        hoverDisposer.dispose();
      };
    }, [monaco]);

    const replaceDocumentImpl = useCallback(
      (text: string) => {
        const ed = editorRef.current;
        if (ed) {
          const model = ed.getModel();
          if (model) {
            // executeEdits over the full document range preserves Monaco's
            // undo stack so the user can revert a template load via Ctrl+Z.
            const lastLine = model.getLineCount();
            const lastCol = model.getLineMaxColumn(lastLine);
            ed.executeEdits("template-replace", [
              {
                range: {
                  startLineNumber: 1,
                  startColumn: 1,
                  endLineNumber: lastLine,
                  endColumn: lastCol,
                },
                text,
                forceMoveMarkers: true,
              },
            ]);
            // Monaco's onChange will fire and propagate via the debounced
            // path; we also seed yamlRef so a quick subsequent insertText
            // sees the new content.
            yamlRef.current = text;
            return;
          }
        }
        // Fallback: pre-mount or test environment. Set state + parse +
        // propagate synchronously so the parent draft updates immediately.
        setYaml(text);
        yamlRef.current = text;
        parseAndPropagate(text);
      },
      [parseAndPropagate],
    );

    useImperativeHandle(
      ref,
      () => ({
        replaceDocument: replaceDocumentImpl,
        insertText(text: string) {
          const ed = editorRef.current;
          if (ed) {
            const isFocused = ed.hasTextFocus();
            if (isFocused) {
              const sel = ed.getSelection();
              if (sel) {
                ed.executeEdits("template-insert", [
                  { range: sel, text, forceMoveMarkers: true },
                ]);
                return;
              }
            }
            const model = ed.getModel();
            if (model) {
              const lastLine = model.getLineCount();
              const lastCol = model.getLineMaxColumn(lastLine);
              const isEmptyDoc = lastLine === 1 && lastCol === 1;
              const prefix = isEmptyDoc ? "" : "\n";
              ed.executeEdits("template-append", [
                {
                  range: {
                    startLineNumber: lastLine,
                    startColumn: lastCol,
                    endLineNumber: lastLine,
                    endColumn: lastCol,
                  },
                  text: prefix + text,
                },
              ]);
              return;
            }
          }
          // Fallback: pre-mount or test environment. Append text directly
          // to the YAML string and synchronously run the parser so the
          // parent draft updates immediately. No debounce — imperative
          // inserts are explicit and tests need deterministic propagation.
          const current = yamlRef.current;
          const sep =
            current.length === 0 || current.endsWith("\n") ? "" : "\n";
          const next = current + sep + text;
          setYaml(next);
          yamlRef.current = next;
          parseAndPropagate(next);
        },
      }),
      [parseAndPropagate, replaceDocumentImpl],
    );

    const monacoOptions = useMemo(
      () => ({
        automaticLayout: true,
        fontFamily: "var(--font-mono, ui-monospace)",
        fontSize: 13,
        lineNumbers: "on" as const,
        minimap: { enabled: false },
        scrollBeyondLastLine: false,
        tabSize: 2,
        wordWrap: "on" as const,
      }),
      [],
    );

    return (
      <div className="flex h-full flex-col">
        <Suspense
          fallback={
            <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
              <Loader2 aria-hidden className="mr-2 h-4 w-4 animate-spin" /> Loading Monaco…
            </div>
          }
        >
          <div className="flex-1 min-h-0">
            <MonacoEditor
              language="yaml"
              value={yaml}
              onChange={handleYamlChange}
              onMount={handleEditorMount}
              options={monacoOptions}
              loading={
                <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                  <Loader2 aria-hidden className="mr-2 h-4 w-4 animate-spin" /> Loading Monaco…
                </div>
              }
              theme="vs-dark"
            />
          </div>
        </Suspense>
        {parseError !== null && (
          <div
            role="alert"
            className="border-t border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning"
          >
            {parseError}
          </div>
        )}
        {parseError === null && schemaIssue !== null && (
          <div
            role="status"
            aria-live="polite"
            className="border-t border-info/40 bg-info/10 px-3 py-2 text-xs text-info"
          >
            {schemaIssue}
          </div>
        )}
        {schema === null && (
          <div
            role="alert"
            className="border-t border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning"
          >
            Schema for this rule type is unavailable; autocomplete and
            schema diagnostics are disabled.
          </div>
        )}
      </div>
    );
  },
);

export default RuleMonacoEditor;
