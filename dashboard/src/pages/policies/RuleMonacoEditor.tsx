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

const MonacoEditor = lazy(() => import("@monaco-editor/react"));

export interface RuleMonacoEditorHandle {
  /**
   * Inserts text at the Monaco cursor when the editor is focused; appends
   * at end-of-document otherwise. When the editor instance hasn't mounted
   * yet (or under the test stub), updates the local YAML state and
   * synchronously runs the parser so the parent draft still updates.
   */
  insertText(text: string): void;
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
            return;
          }
          setParseError(null);
          if (parsed.rule) {
            lastEmittedRef.current = parsed.rule;
            onChange(parsed.rule);
          }
        } catch (err) {
          setParseError(err instanceof Error ? err.message : "YAML parse failed");
          onError?.(err);
          logger.warn("policy-studio-editor", "yaml parse failure", { err });
        }
      },
      [onChange, onError, rule],
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

    useImperativeHandle(
      ref,
      () => ({
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
      [parseAndPropagate],
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
      </div>
    );
  },
);

export default RuleMonacoEditor;
