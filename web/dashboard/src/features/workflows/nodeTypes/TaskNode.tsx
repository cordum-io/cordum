import { Handle, Position, type NodeProps } from "reactflow";
import { Cylinder } from "lucide-react";
import Badge from "../../../components/Badge";
import { useRunStore } from "../../../state/runStore";
import type { TaskNodeData } from "../types";

export default function TaskNode({ id, data, selected }: NodeProps<TaskNodeData>) {
  const activeRunId = useRunStore((s) => s.activeRunId);
  const nodeState = useRunStore((s) => (activeRunId ? s.runsById[activeRunId]?.nodeResults[id]?.state : null));

  return (
    <div
      className={[
        "min-w-[240px] rounded-2xl border bg-secondary-background/60 px-3 py-2 text-xs shadow-sm backdrop-blur",
        selected ? "border-primary" : "border-primary-border",
      ].join(" ")}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <div className="rounded-lg border border-primary/20 bg-primary/10 p-1 text-secondary-text">
            <Cylinder size={14} />
          </div>
          <div className="text-[10px] uppercase tracking-wider text-tertiary-text">Task</div>
        </div>
        {nodeState ? <Badge state={nodeState} /> : null}
      </div>
      <div className="mt-1 truncate font-semibold text-primary-text">{data.name || "Task"}</div>
      <div className="mt-2 flex items-center gap-2">
        <div className="max-w-[200px] truncate rounded-md border border-primary-border bg-tertiary-background px-2 py-0.5 font-mono text-[11px] text-tertiary-text">
          {data.topic || "-"}
        </div>
      </div>
      <Handle type="target" position={Position.Left} className="!h-2.5 !w-2.5 !border-0 !bg-primary" />
      <Handle type="source" position={Position.Right} className="!h-2.5 !w-2.5 !border-0 !bg-primary" />
    </div>
  );
}
