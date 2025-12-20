import { Handle, Position, type NodeProps } from "reactflow";
import { Database } from "lucide-react";
import type { MemoryNodeData } from "../types";
import { useRunStore } from "../../../state/runStore";
import { useWorkflowStore } from "../../../state/workflowStore";
import { useInspectorStore } from "../../../state/inspectorStore";
import MemoryPointerViewer from "../../../components/MemoryPointerViewer";

function labelForStrategy(strategy: MemoryNodeData["strategy"]) {
  switch (strategy) {
    case "run":
      return "Per run";
    case "workflow":
      return "Per workflow";
    case "custom":
      return "Custom";
    default:
      return "Per run";
  }
}

export default function MemoryNode({ data, selected }: NodeProps<MemoryNodeData>) {
  const showInspector = useInspectorStore((s) => s.show);
  const selectedWorkflowId = useWorkflowStore((s) => s.selectedWorkflowId);
  const activeRun = useRunStore((s) => {
    const id = s.activeRunId;
    if (!id) return null;
    const run = s.runsById[id];
    if (!run) return null;
    if (selectedWorkflowId && run.workflowId !== selectedWorkflowId) return null;
    return run;
  });

  const configuredMemoryId =
    data.strategy === "workflow" && selectedWorkflowId
      ? `wf:${selectedWorkflowId}`
      : data.strategy === "custom"
        ? data.customMemoryId.trim()
        : "";

  const memoryId = activeRun?.memoryId?.trim() || configuredMemoryId;

  return (
    <div
      className={[
        "min-w-[240px] rounded-2xl border bg-secondary-background/60 px-3 py-2 text-xs shadow-sm backdrop-blur",
        selected ? "border-violet-400/60" : "border-primary-border",
      ].join(" ")}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <div className="rounded-lg border border-violet-400/20 bg-violet-500/10 p-1 text-violet-200">
            <Database size={14} />
          </div>
          <div>
            <div className="text-[10px] uppercase tracking-wider text-tertiary-text">Memory</div>
            <div className="truncate font-semibold text-primary-text">{data.name || "Memory"}</div>
          </div>
        </div>
        <div className="rounded-md border border-primary-border bg-tertiary-background px-2 py-0.5 text-[11px] text-tertiary-text">
          {labelForStrategy(data.strategy)}
        </div>
      </div>

      <div className="mt-2">
        {memoryId ? (
          <div className="flex items-center justify-between gap-2">
            <div className="min-w-0 truncate font-mono text-[11px] text-tertiary-text" title={memoryId}>
              {memoryId}
            </div>
            <div className="flex shrink-0 items-center gap-1">
              <button
                type="button"
                className="rounded-md border border-white/10 bg-black/20 px-2 py-1 text-[10px] text-zinc-300 hover:bg-black/30"
                onClick={() => showInspector("Memory: Events (context engine)", <MemoryPointerViewer pointer={`redis://mem:${memoryId}:events`} />)}
                title={`redis://mem:${memoryId}:events`}
              >
                events
              </button>
              <button
                type="button"
                className="rounded-md border border-white/10 bg-black/20 px-2 py-1 text-[10px] text-zinc-300 hover:bg-black/30"
                onClick={() => showInspector("Memory: Chunks Index (context engine)", <MemoryPointerViewer pointer={`redis://mem:${memoryId}:chunks`} />)}
                title={`redis://mem:${memoryId}:chunks`}
              >
                chunks
              </button>
            </div>
          </div>
        ) : (
          <div className="text-[11px] text-tertiary-text">
            {data.strategy === "run"
              ? "Runs use memory_id = run:<run_id>."
              : data.strategy === "workflow"
                ? "Set workflow id first."
                : "Set a custom memory id."}
          </div>
        )}
      </div>

      <Handle type="target" position={Position.Left} className="!h-2.5 !w-2.5 !border-0 !bg-violet-300" />
      <Handle type="source" position={Position.Right} className="!h-2.5 !w-2.5 !border-0 !bg-violet-300" />
    </div>
  );
}

