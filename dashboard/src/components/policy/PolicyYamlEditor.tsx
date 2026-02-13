import { useCallback, useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import Editor, { DiffEditor, loader } from "@monaco-editor/react";
import type { editor } from "monaco-editor";
import { Loader, AlertTriangle, GitCompare, FileCode } from "lucide-react";
import { put } from "../../api/client";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";
import {
  validatePolicyYaml,
  countRulesFromYaml,
} from "../../lib/policy-yaml";
import { usePolicyBundle, encodePolicyBundleId } from "../../hooks/usePolicies";

const MONACO_BASE_PATH = "/monaco/vs";
loader.config({ paths: { vs: MONACO_BASE_PATH } });

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

interface PolicyYamlEditorProps {
  bundleId: string;
}

export function PolicyYamlEditor({ bundleId }: PolicyYamlEditorProps) {
  const queryClient = useQueryClient();
  const { data: bundle, isLoading, error } = usePolicyBundle(bundleId);
  const editable = bundleId.startsWith("secops/");

  // The YAML content the user is editing
  const [yamlContent, setYamlContent] = useState("");
  // Snapshot of the YAML when the bundle was last loaded (for diff)
  const [publishedYaml, setPublishedYaml] = useState("");
  // Validation errors
  const [validationErrors, setValidationErrors] = useState<
    Array<{ line: number; message: string }>
  >([]);
  // Diff mode toggle
  const [showDiff, setShowDiff] = useState(false);
  // Dirty flag
  const [isDirty, setIsDirty] = useState(false);

  const editorRef = useRef<editor.IStandaloneCodeEditor | null>(null);
  const monacoRef = useRef<typeof import("monaco-editor") | null>(null);

  // Sync content → YAML when bundle data changes
  useEffect(() => {
    if (bundle?.content !== undefined) {
      const yaml = bundle.content ?? "";
      setYamlContent(yaml);
      setPublishedYaml(yaml);
      setIsDirty(false);
      setValidationErrors([]);
    }
  }, [bundle]);

  // Update error markers in Monaco
  useEffect(() => {
    const model = editorRef.current?.getModel();
    const monaco = monacoRef.current;
    if (!model || !monaco) return;

    const markers: editor.IMarkerData[] = validationErrors.map((err) => ({
      severity: monaco.MarkerSeverity.Error,
      message: err.message,
      startLineNumber: err.line,
      startColumn: 1,
      endLineNumber: err.line,
      endColumn: model.getLineMaxColumn(err.line) || 1,
    }));
    monaco.editor.setModelMarkers(model, "policy-yaml", markers);
  }, [validationErrors]);

  // Handle YAML content change
  const handleChange = useCallback(
    (value: string | undefined) => {
      const v = value ?? "";
      setYamlContent(v);
      setIsDirty(v !== publishedYaml);

      const result = validatePolicyYaml(v);
      setValidationErrors(result.errors);
    },
    [publishedYaml],
  );

  // Save mutation — PUT YAML content back to bundle
  const saveMutation = useMutation({
    mutationFn: async (yaml: string) => {
      const safeId = encodePolicyBundleId(bundleId);
      await put(`/policy/bundles/${safeId}`, { content: yaml });
      return yaml;
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["policy-bundle", bundleId] });
      queryClient.invalidateQueries({ queryKey: ["policy-bundles"] });
      setIsDirty(false);
    },
  });

  const handleApply = useCallback(() => {
    const result = validatePolicyYaml(yamlContent);
    if (!result.valid) return;
    if (!editable) return;
    saveMutation.mutate(yamlContent);
  }, [yamlContent, saveMutation, editable]);

  const handleEditorMount = useCallback(
    (ed: editor.IStandaloneCodeEditor, monaco: typeof import("monaco-editor")) => {
      editorRef.current = ed;
      monacoRef.current = monaco;
    },
    [],
  );

  // ---------------------------------------------------------------------------
  // Render
  // ---------------------------------------------------------------------------

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-16 text-sm text-muted">
        <Loader className="mr-2 h-4 w-4 animate-spin" />
        Loading policy bundle...
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

  const hasErrors = validationErrors.length > 0;
  const ruleCount = !hasErrors ? countRulesFromYaml(yamlContent) : 0;

  return (
    <div className="space-y-3">
      {/* Header bar */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h3 className="font-display text-lg font-semibold text-ink">
            YAML Editor
          </h3>
          <Badge variant="info">{ruleCount} rule{ruleCount !== 1 ? "s" : ""}</Badge>
          {isDirty && (
            <Badge variant="warning">Unsaved changes</Badge>
          )}
          {!editable && (
            <Badge variant="default">Read-only</Badge>
          )}
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            type="button"
            onClick={() => setShowDiff(!showDiff)}
          >
            {showDiff ? (
              <>
                <FileCode className="h-3.5 w-3.5" />
                Editor
              </>
            ) : (
              <>
                <GitCompare className="h-3.5 w-3.5" />
                Diff
              </>
            )}
          </Button>
          <Button
            size="sm"
            type="button"
            disabled={hasErrors || !isDirty || saveMutation.isPending || !editable}
            onClick={handleApply}
          >
            {saveMutation.isPending ? (
              <Loader className="h-3.5 w-3.5 animate-spin" />
            ) : (
              "Apply Changes"
            )}
          </Button>
        </div>
      </div>

      {/* Error summary */}
      {hasErrors && (
        <div className="flex items-start gap-2 rounded-xl border border-danger/30 bg-danger/5 px-4 py-3">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-danger" />
          <div className="space-y-1 text-xs text-danger">
            {validationErrors.map((err, i) => (
              <p key={i}>
                Line {err.line}: {err.message}
              </p>
            ))}
          </div>
        </div>
      )}

      {!editable && (
        <div className="rounded-xl border border-border bg-surface2/40 px-4 py-3 text-xs text-muted">
          This bundle is managed by a pack and is read-only. Create or edit a
          bundle under `secops/` to make changes.
        </div>
      )}

      {/* Save error */}
      {saveMutation.isError && (
        <div className="flex items-center gap-2 rounded-xl border border-danger/30 bg-danger/5 px-4 py-3 text-xs text-danger">
          <AlertTriangle className="h-4 w-4 shrink-0" />
          Failed to save: {saveMutation.error?.message ?? "Unknown error"}
        </div>
      )}

      {/* Editor */}
      <div className="overflow-hidden rounded-xl border border-border">
        {showDiff ? (
          <DiffEditor
            height="500px"
            language="yaml"
            original={publishedYaml}
            modified={yamlContent}
            theme="vs-dark"
            options={{
              readOnly: !editable,
              minimap: { enabled: false },
              fontSize: 13,
              lineNumbers: "on",
              scrollBeyondLastLine: false,
              wordWrap: "on",
              renderSideBySide: true,
            }}
          />
        ) : (
          <Editor
            height="500px"
            language="yaml"
            value={yamlContent}
            theme="vs-dark"
            onChange={handleChange}
            onMount={handleEditorMount}
            options={{
              readOnly: !editable,
              minimap: { enabled: false },
              fontSize: 13,
              lineNumbers: "on",
              scrollBeyondLastLine: false,
              wordWrap: "on",
              tabSize: 2,
              automaticLayout: true,
            }}
          />
        )}
      </div>
    </div>
  );
}
