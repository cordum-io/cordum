import { useRef } from "react";
import { Copy } from "lucide-react";
import { toast } from "sonner";

interface BundleYamlEditorProps {
  yaml: string;
  editable: boolean;
  onChange: (nextYaml: string) => void;
}

export function BundleYamlEditor({ yaml, editable, onChange }: BundleYamlEditorProps) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  return (
    <div className="space-y-3">
      {!editable && (
        <div className="rounded-2xl border border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10 px-3 py-2 text-xs text-[var(--color-warning)]">
          YAML is read-only for your role.
        </div>
      )}

      <div className="flex items-center justify-between">
        <p className="text-[10px] font-mono uppercase tracking-wider text-muted-foreground">
          bundle content
        </p>
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

      <div className="instrument-card p-0">
        <textarea
          ref={textareaRef}
          aria-label="Bundle YAML editor"
          className="h-[520px] w-full resize-none rounded-lg bg-surface-0 p-4 font-mono text-xs text-foreground outline-none focus:ring-2 focus:ring-cordum/30"
          value={yaml}
          readOnly={!editable}
          onChange={(event) => onChange(event.target.value)}
        />
      </div>
    </div>
  );
}
