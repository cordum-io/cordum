import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Loader2 } from "lucide-react";
import type { NormalizedRule } from "@/hooks/useRulesList";
import { logger } from "@/lib/logger";
import { ruleToYaml, yamlToPartialRule } from "@/lib/policy-studio/editor/yaml";

const MonacoEditor = lazy(() => import("@monaco-editor/react"));

interface RuleMonacoEditorProps {
  rule: NormalizedRule;
  onChange: (rule: NormalizedRule) => void;
  onError?: (err: unknown) => void;
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
function RuleMonacoEditor({ rule, onChange, onError }: RuleMonacoEditorProps) {
  // Initial YAML serialization happens once on mount and then again each
  // time the parent updates the rule (e.g. Form view edits, template
  // insertion). The lastSerializedFrom ref prevents a write-loop: after we
  // emit `onChange(rule)`, the parent re-renders with the same rule and
  // we'd otherwise re-serialize and overwrite the user's in-flight edits.
  const [yaml, setYaml] = useState<string>(() => ruleToYaml(rule));
  const lastEmittedRef = useRef<NormalizedRule | null>(rule);
  const debounceRef = useRef<number | undefined>(undefined);
  const [parseError, setParseError] = useState<string | null>(null);

  useEffect(() => {
    if (lastEmittedRef.current === rule) {
      // We just emitted this rule — Monaco already has the matching YAML.
      return;
    }
    setYaml(ruleToYaml(rule));
    lastEmittedRef.current = rule;
  }, [rule]);

  useEffect(() => {
    return () => {
      if (debounceRef.current !== undefined) {
        window.clearTimeout(debounceRef.current);
      }
    };
  }, []);

  const handleYamlChange = useCallback(
    (next: string | undefined) => {
      const text = next ?? "";
      setYaml(text);
      if (debounceRef.current !== undefined) {
        window.clearTimeout(debounceRef.current);
      }
      debounceRef.current = window.setTimeout(() => {
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
      }, ONCHANGE_DEBOUNCE_MS);
    },
    [onChange, onError, rule],
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
}

export default RuleMonacoEditor;
