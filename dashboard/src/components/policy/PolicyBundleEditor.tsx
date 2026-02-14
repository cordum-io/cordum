import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Editor, { DiffEditor, loader } from "@monaco-editor/react";
import type { editor as MonacoEditor } from "monaco-editor";
import {
  AlertTriangle,
  FileCode,
  GitCompare,
  Loader,
  Save,
  X,
} from "lucide-react";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";
import { Card } from "../ui/Card";
import {
  countRulesFromYaml,
  validatePolicyYaml,
} from "../../lib/policy-yaml";
import { useUpdatePolicyBundle } from "../../hooks/usePolicies";

const MONACO_BASE_PATH = "/monaco/vs";
loader.config({ paths: { vs: MONACO_BASE_PATH } });

interface PolicyBundleEditorProps {
  bundleId: string;
  currentContent: string;
  onClose: () => void;
}

type ConfirmAction = "save" | "discard" | null;

function ConfirmDialog({
  action,
  isPending,
  onConfirm,
  onCancel,
}: {
  action: Exclude<ConfirmAction, null>;
  isPending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const isSave = action === "save";
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <Card className="relative z-10 w-full max-w-md">
        <div className="space-y-4">
          <h3 className="font-display text-lg font-semibold text-ink">
            {isSave ? "Update Live Policy Bundle" : "Discard YAML Changes"}
          </h3>
          <p className="text-sm text-muted">
            {isSave
              ? "This will update the live policy bundle and affect safety evaluation behavior."
              : "You have unsaved YAML changes. Discard and close the editor?"}
          </p>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={onCancel} disabled={isPending}>
              Cancel
            </Button>
            <Button
              variant={isSave ? "primary" : "danger"}
              size="sm"
              onClick={onConfirm}
              disabled={isPending}
            >
              {isPending
                ? isSave
                  ? "Saving..."
                  : "Closing..."
                : isSave
                  ? "Save Bundle"
                  : "Discard Changes"}
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}

export function PolicyBundleEditor({
  bundleId,
  currentContent,
  onClose,
}: PolicyBundleEditorProps) {
  const [yamlContent, setYamlContent] = useState(currentContent ?? "");
  const [savedContent, setSavedContent] = useState(currentContent ?? "");
  const [showDiff, setShowDiff] = useState(false);
  const [confirmAction, setConfirmAction] = useState<ConfirmAction>(null);
  const [validationErrors, setValidationErrors] = useState<
    Array<{ line: number; message: string }>
  >([]);

  const editorRef = useRef<MonacoEditor.IStandaloneCodeEditor | null>(null);
  const monacoRef = useRef<typeof import("monaco-editor") | null>(null);

  const updateBundle = useUpdatePolicyBundle();

  useEffect(() => {
    setYamlContent(currentContent ?? "");
    setSavedContent(currentContent ?? "");
    setValidationErrors([]);
  }, [currentContent, bundleId]);

  useEffect(() => {
    const model = editorRef.current?.getModel();
    const monaco = monacoRef.current;
    if (!model || !monaco) return;
    const markers: MonacoEditor.IMarkerData[] = validationErrors.map((err) => ({
      severity: monaco.MarkerSeverity.Error,
      message: err.message,
      startLineNumber: Math.max(1, err.line),
      startColumn: 1,
      endLineNumber: Math.max(1, err.line),
      endColumn: model.getLineMaxColumn(Math.max(1, err.line)),
    }));
    monaco.editor.setModelMarkers(model, "policy-yaml", markers);
  }, [validationErrors]);

  const isModified = yamlContent !== savedContent;
  const parsed = useMemo(() => validatePolicyYaml(yamlContent), [yamlContent]);
  const errorCount = validationErrors.length;
  const lineCount = useMemo(() => {
    if (!yamlContent) return 0;
    return yamlContent.split(/\r?\n/).length;
  }, [yamlContent]);
  const charCount = yamlContent.length;
  const ruleCount = useMemo(
    () => (parsed.valid ? countRulesFromYaml(yamlContent) : 0),
    [parsed.valid, yamlContent],
  );

  const handleValidate = useCallback((next: string) => {
    const validation = validatePolicyYaml(next);
    setValidationErrors(validation.errors);
  }, []);

  const handleChange = useCallback(
    (value: string | undefined) => {
      const next = value ?? "";
      setYamlContent(next);
      handleValidate(next);
    },
    [handleValidate],
  );

  const requestSave = useCallback(() => {
    if (updateBundle.isPending || !isModified) return;
    const trimmed = yamlContent.trim();
    if (!trimmed) {
      setValidationErrors([{ line: 1, message: "content required" }]);
      return;
    }
    const validation = validatePolicyYaml(trimmed);
    setValidationErrors(validation.errors);
    if (!validation.valid) return;
    setConfirmAction("save");
  }, [isModified, updateBundle.isPending, yamlContent]);

  const handleConfirmSave = useCallback(() => {
    const trimmed = yamlContent.trim();
    if (!trimmed) return;
    updateBundle.mutate(
      { id: bundleId, content: trimmed },
      {
        onSuccess: () => {
          setSavedContent(trimmed);
          setConfirmAction(null);
          onClose();
        },
        onError: () => {
          setConfirmAction(null);
        },
      },
    );
  }, [bundleId, onClose, updateBundle, yamlContent]);

  const requestClose = useCallback(() => {
    if (updateBundle.isPending) return;
    if (!isModified) {
      onClose();
      return;
    }
    setConfirmAction("discard");
  }, [isModified, onClose, updateBundle.isPending]);

  const handleConfirmDiscard = useCallback(() => {
    setYamlContent(savedContent);
    setValidationErrors([]);
    setConfirmAction(null);
    onClose();
  }, [onClose, savedContent]);

  const handleEditorMount = useCallback(
    (
      mountedEditor: MonacoEditor.IStandaloneCodeEditor,
      monaco: typeof import("monaco-editor"),
    ) => {
      editorRef.current = mountedEditor;
      monacoRef.current = monaco;
      mountedEditor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
        requestSave();
      });
    },
    [requestSave],
  );

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <h3 className="font-display text-lg font-semibold text-ink">Bundle YAML Editor</h3>
          <Badge variant={isModified ? "warning" : "info"}>
            {isModified ? "Modified" : "Saved"}
          </Badge>
          <Badge variant={errorCount > 0 ? "danger" : "success"}>
            {errorCount > 0 ? `${errorCount} error${errorCount > 1 ? "s" : ""}` : "Valid YAML"}
          </Badge>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            type="button"
            onClick={() => setShowDiff((v) => !v)}
          >
            {showDiff ? <FileCode className="h-3.5 w-3.5" /> : <GitCompare className="h-3.5 w-3.5" />}
            {showDiff ? "Hide Changes" : "Show Changes"}
          </Button>
          <Button
            size="sm"
            type="button"
            onClick={requestSave}
            disabled={!isModified || updateBundle.isPending}
          >
            {updateBundle.isPending ? (
              <Loader className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Save className="h-3.5 w-3.5" />
            )}
            Save
          </Button>
          <Button variant="ghost" size="sm" type="button" onClick={requestClose}>
            <X className="h-3.5 w-3.5" />
            Close
          </Button>
        </div>
      </div>

      {validationErrors.length > 0 && (
        <div className="rounded-xl border border-danger/30 bg-danger/5 px-4 py-3 text-xs text-danger">
          <div className="mb-1 flex items-center gap-1.5 font-semibold">
            <AlertTriangle className="h-3.5 w-3.5" />
            YAML validation errors
          </div>
          {validationErrors.map((err, idx) => (
            <div key={`${err.line}-${idx}`}>
              Line {err.line}: {err.message}
            </div>
          ))}
        </div>
      )}

      {updateBundle.isError && (
        <div className="rounded-xl border border-danger/30 bg-danger/5 px-4 py-3 text-xs text-danger">
          Failed to update policy bundle: {updateBundle.error?.message ?? "Unknown error"}
        </div>
      )}

      <div className="overflow-hidden rounded-xl border border-border">
        {showDiff ? (
          <DiffEditor
            height="500px"
            language="yaml"
            original={savedContent}
            modified={yamlContent}
            theme="vs-dark"
            options={{
              readOnly: updateBundle.isPending,
              minimap: { enabled: false },
              fontSize: 13,
              lineNumbers: "on",
              scrollBeyondLastLine: false,
              wordWrap: "on",
              renderSideBySide: true,
              automaticLayout: true,
            }}
          />
        ) : (
          <Editor
            height="500px"
            language="yaml"
            value={yamlContent}
            onChange={handleChange}
            onMount={handleEditorMount}
            theme="vs-dark"
            options={{
              readOnly: updateBundle.isPending,
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

      <div className="rounded-xl border border-border bg-surface2/30 px-4 py-2 text-xs text-muted">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
          <span>{lineCount} lines</span>
          <span>{charCount} chars</span>
          <span>{ruleCount} rule{ruleCount === 1 ? "" : "s"}</span>
          <span>{errorCount} error{errorCount === 1 ? "" : "s"}</span>
          <span>Ctrl/Cmd+S to save</span>
        </div>
        <p className="mt-1">
          Help: use valid policy YAML (for example top-level `rules` / `output_rules` sections). Server-side schema validation is applied on save.
        </p>
      </div>

      {confirmAction === "save" && (
        <ConfirmDialog
          action="save"
          isPending={updateBundle.isPending}
          onConfirm={handleConfirmSave}
          onCancel={() => setConfirmAction(null)}
        />
      )}
      {confirmAction === "discard" && (
        <ConfirmDialog
          action="discard"
          isPending={false}
          onConfirm={handleConfirmDiscard}
          onCancel={() => setConfirmAction(null)}
        />
      )}
    </div>
  );
}
