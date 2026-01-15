import { memo } from "react";
import { Handle, Position, type NodeProps } from "reactflow";
import type { WorkerNodeData } from "../types";

function WorkerNodeComponent({ id, data, selected }: NodeProps<WorkerNodeData>) {
  return (
    <div
      className={`builder-node builder-node--worker ${selected ? "builder-node--selected" : ""}`}
      onClick={() => data.onSelect(id)}
    >
      <Handle type="target" position={Position.Left} className="builder-handle" />

      <div className="builder-node__header">
        <div className="builder-node__icon bg-accent">WO</div>
        <div className="builder-node__info">
          <div className="builder-node__label">{data.label}</div>
          <div className="builder-node__type">Worker</div>
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
        {data.topic && (
          <div className="builder-node__field">
            <span className="builder-node__field-label">Topic:</span>
            <span className="builder-node__field-value">{data.topic}</span>
          </div>
        )}
        {data.packId && (
          <div className="builder-node__field">
            <span className="builder-node__field-label">Pack:</span>
            <span className="builder-node__field-value">{data.packId}</span>
          </div>
        )}
        {data.capability && (
          <div className="builder-node__field">
            <span className="builder-node__field-label">Capability:</span>
            <span className="builder-node__field-value">{data.capability}</span>
          </div>
        )}
        {data.riskTags && data.riskTags.length > 0 && (
          <div className="builder-node__tags">
            {data.riskTags.map((tag) => (
              <span key={tag} className="builder-node__tag builder-node__tag--risk">
                {tag}
              </span>
            ))}
          </div>
        )}
      </div>

      <Handle type="source" position={Position.Right} id="output" className="builder-handle" />
    </div>
  );
}

export const WorkerNode = memo(WorkerNodeComponent);
