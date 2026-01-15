import { memo } from "react";
import { Handle, Position, type NodeProps } from "reactflow";
import type { SubworkflowNodeData } from "../types";

function SubworkflowNodeComponent({ id, data, selected }: NodeProps<SubworkflowNodeData>) {
  return (
    <div
      className={`builder-node builder-node--subworkflow ${selected ? "builder-node--selected" : ""}`}
      onClick={() => data.onSelect(id)}
    >
      <Handle type="target" position={Position.Left} className="builder-handle" />

      <div className="builder-node__header">
        <div className="builder-node__icon bg-indigo-500">SW</div>
        <div className="builder-node__info">
          <div className="builder-node__label">{data.label}</div>
          <div className="builder-node__type">Subworkflow</div>
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
        {data.subworkflowId ? (
          <div className="builder-node__field">
            <span className="builder-node__field-label">Workflow:</span>
            <span className="builder-node__field-value builder-node__field-value--mono">
              {data.subworkflowId}
            </span>
          </div>
        ) : (
          <div className="builder-node__empty">
            No workflow selected
          </div>
        )}
        {data.input && Object.keys(data.input).length > 0 && (
          <div className="builder-node__field">
            <span className="builder-node__field-label">Input:</span>
            <span className="builder-node__field-value">
              {Object.keys(data.input).length} fields
            </span>
          </div>
        )}
      </div>

      <Handle type="source" position={Position.Right} id="output" className="builder-handle" />
    </div>
  );
}

export const SubworkflowNode = memo(SubworkflowNodeComponent);
