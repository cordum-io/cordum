import { useState } from "react";
import { ArrowRight, ChevronDown, CheckCircle2, AlertOctagon, XCircle, AlertTriangle } from "lucide-react";
import { Badge } from "@/components/ui/Badge";
import { CodeBlock } from "@/components/ui/CodeBlock";
import { ConstraintsPanel } from "@/components/governance/ConstraintsPanel";
import type { EvalEntryResult, EvalRunStatus, PolicyConstraints } from "@/api/types";
import { cn } from "@/lib/utils";

const STATUS_VARIANT: Record<EvalRunStatus, "success" | "warning" | "danger" | "default"> = {
  pass: "success",
  fail: "warning",
  regression: "danger",
  error: "default",
};

const STATUS_ICON: Record<EvalRunStatus, typeof CheckCircle2> = {
  pass: CheckCircle2,
  fail: XCircle,
  regression: AlertOctagon,
  error: AlertTriangle,
};

const DRIFT_COLOR: Record<EvalEntryResult["driftDirection"], string> = {
  escalated: "text-danger",
  relaxed: "text-warning",
  unchanged: "text-muted-foreground",
};

export function EntryResultRow({ entry }: { entry: EvalEntryResult }) {
  const [open, setOpen] = useState(false);
  const StatusIcon = STATUS_ICON[entry.status];
  const statusVariant = STATUS_VARIANT[entry.status];
  const driftClass = DRIFT_COLOR[entry.driftDirection];

  return (
    <div className="rounded-xl border border-border bg-surface-1" data-testid="entry-row">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between gap-3 px-3 py-2 text-left hover:bg-surface-2/50"
      >
        <span className="flex min-w-0 items-center gap-3">
          <StatusIcon className={cn("h-4 w-4 shrink-0", driftClass)} />
          <Badge variant={statusVariant} aria-label={`status ${entry.status}`}>
            {entry.status}
          </Badge>
          <span className="font-mono text-xs text-muted-foreground truncate">{entry.entryId}</span>
          {entry.ruleId && (
            <span className="font-mono text-xs text-muted-foreground truncate">· {entry.ruleId}</span>
          )}
        </span>
        <span className="flex shrink-0 items-center gap-2">
          <span
            className={cn("flex items-center gap-1 font-mono text-xs uppercase", driftClass)}
            data-drift={entry.driftDirection}
          >
            {String(entry.expectedDecision).toUpperCase()}
            <ArrowRight className="h-3 w-3" />
            {String(entry.actualDecision).toUpperCase()}
          </span>
          <ChevronDown className={cn("h-4 w-4 text-muted-foreground transition-transform", open && "rotate-180")} />
        </span>
      </button>
      {open && (
        <div className="space-y-3 border-t border-border px-3 py-3">
          {entry.reason && (
            <p className="text-xs text-muted-foreground">
              <span className="font-mono uppercase tracking-wider">Reason:</span> {entry.reason}
            </p>
          )}
          <CodeBlock title="Input snapshot" language="json" maxHeight={240}>
            {JSON.stringify(entry.input, null, 2)}
          </CodeBlock>
          {entry.constraints && Object.keys(entry.constraints).length > 0 && (
            <ConstraintsPanel constraints={entry.constraints as PolicyConstraints} />
          )}
        </div>
      )}
    </div>
  );
}
