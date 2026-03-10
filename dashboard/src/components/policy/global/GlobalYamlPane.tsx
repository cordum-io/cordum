import { useMemo, useRef } from "react";
import { Copy, LocateFixed } from "lucide-react";
import { toast } from "sonner";
import type { GlobalPolicyParseIssue } from "@/types/policy";

interface GlobalYamlPaneProps {
  yaml: string;
  editable?: boolean;
  activeRuleId?: string | null;
  parseIssues: GlobalPolicyParseIssue[];
  onChange: (nextYaml: string) => void;
}

function findRuleLine(yaml: string, ruleId: string | null | undefined): number | null {
  if (!ruleId) return null;
  const lines = yaml.split(/\r?\n/);
  const needle = `- id: ${ruleId}`;
  const index = lines.findIndex((line) => line.trim() === needle || line.includes(needle));
  return index >= 0 ? index + 1 : null;
}

export function GlobalYamlPane({
  yaml,
  editable = true,
  activeRuleId,
  parseIssues,
  onChange,
}: GlobalYamlPaneProps) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const activeLine = useMemo(() => findRuleLine(yaml, activeRuleId), [yaml, activeRuleId]);
  const hasErrors = parseIssues.some((issue) => issue.severity === "error");

  const jumpToActiveRule = () => {
    if (!textareaRef.current || !activeLine) return;
    const lines = yaml.split(/\r?\n/);
    const lineIndex = Math.max(activeLine - 1, 0);
    const start = lines.slice(0, lineIndex).reduce((sum, line) => sum + line.length + 1, 0);
    const end = start + (lines[lineIndex]?.length ?? 0);
    textareaRef.current.focus();
    textareaRef.current.setSelectionRange(start, end);
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <p className="text-[10px] font-mono uppercase tracking-wider text-muted-foreground">
          safety.yaml
        </p>
        <div className="flex items-center gap-1">
          {activeRuleId && activeLine && (
            <button
              className="inline-flex items-center gap-1 rounded border border-border bg-surface-1 px-2 py-1 text-[10px] font-mono text-muted-foreground hover:text-foreground"
              onClick={jumpToActiveRule}
            >
              <LocateFixed className="h-3 w-3" />
              {activeRuleId} @ line {activeLine}
            </button>
          )}
          <button
            className="inline-flex items-center gap-1 rounded border border-border bg-surface-1 px-2 py-1 text-[10px] font-mono text-muted-foreground hover:text-foreground"
            onClick={() => {
              navigator.clipboard.writeText(yaml);
              toast.success("YAML copied");
            }}
          >
            <Copy className="h-3 w-3" />
            Copy
          </button>
        </div>
      </div>

      <div className="instrument-card p-0">
        <textarea
          ref={textareaRef}
          aria-label="Global safety.yaml editor"
          className="h-[620px] w-full resize-none rounded-lg bg-surface-0 p-4 font-mono text-xs text-foreground outline-none focus:ring-2 focus:ring-cordum/30"
          value={yaml}
          readOnly={!editable}
          onChange={(event) => onChange(event.target.value)}
        />
      </div>

      {parseIssues.length > 0 && (
        <div
          className={`rounded-lg border px-3 py-2 text-xs ${
            hasErrors
              ? "border-destructive/30 bg-destructive/10 text-destructive"
              : "border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10 text-[var(--color-warning)]"
          }`}
        >
          <p className="mb-1 font-medium">
            {hasErrors ? "YAML validation errors" : "YAML validation warnings"}
          </p>
          <ul className="space-y-1">
            {parseIssues.map((issue, index) => (
              <li key={`${issue.path}-${issue.message}-${index}`}>
                {issue.line ? `line ${issue.line}${issue.column ? `:${issue.column}` : ""} — ` : ""}
                {issue.path}: {issue.message}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

