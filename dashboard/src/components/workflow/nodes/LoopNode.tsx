import { memo } from "react";
import { Handle, Position, type NodeProps } from "reactflow";
import type { LoopNodeData } from "../types";

function LoopNodeComponent({ id, data, selected }: NodeProps<LoopNodeData>) {
  return (
    <div
      className={`builder-node builder-node--loop ${selected ? "builder-node--selected" : ""}`}
      onClick={() => data.onSelect(id)}
    >
      <Handle type="target" position={Position.Left} className="builder-handle" />

      <div className="builder-node__header">
        <div className="builder-node__icon bg-purple-500">LP</div>
        <div className="builder-node__info">
          <div className="builder-node__label">{data.label}</div>
          <div className="builder-node__type">Loop</div>
        </div>
        <button
          onClick={(e) => {
            e.stopPropagation();
            data.onDelete(id);
          }}
          className="builder-node__delete"
        >
          &times;
        </button>
      </div>

      <div className="builder-node__body">
        <div className="builder-node__field">
          <span className="builder-node__field-label">ForEach:</span>
          <code className="text-[10px]">{data.forEach || "{{ items }}"}</code>
        </div>
        {data.maxParallel && data.maxParallel > 1 && (
          <div className="builder-node__field">
            <span className="builder-node__field-label">Parallel:</span>
            <span className="builder-node__field-value">{data.maxParallel}</span>
          </div>
        )}
      </div>

      <Handle type="source" position={Position.Right} id="output" className="builder-handle" />
    </div>
  );
}

export const LoopNode = memo(LoopNodeComponent);
