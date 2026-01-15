import { memo } from "react";
import { Handle, Position, type NodeProps } from "reactflow";
import type { ParallelNodeData } from "../types";

function ParallelNodeComponent({ id, data, selected }: NodeProps<ParallelNodeData>) {
  return (
    <div
      className={`builder-node builder-node--parallel ${selected ? "builder-node--selected" : ""}`}
      onClick={() => data.onSelect(id)}
    >
      <Handle type="target" position={Position.Left} className="builder-handle" />

      <div className="builder-node__header">
        <div className="builder-node__icon bg-cyan-500">PA</div>
        <div className="builder-node__info">
          <div className="builder-node__label">{data.label}</div>
          <div className="builder-node__type">Parallel</div>
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
          <span className="builder-node__field-label">Branches:</span>
          <span className="builder-node__field-value">
            {data.branches?.length || 0}
          </span>
        </div>
        <div className="builder-node__field">
          <span className="builder-node__field-label">Wait All:</span>
          <span className="builder-node__field-value">
            {data.waitAll !== false ? "Yes" : "No"}
          </span>
        </div>
      </div>

      <Handle type="source" position={Position.Right} id="output" className="builder-handle" />
    </div>
  );
}

export const ParallelNode = memo(ParallelNodeComponent);
