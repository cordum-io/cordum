import { memo } from "react";
import { Handle, Position, type NodeProps } from "reactflow";
import type { ConditionNodeData } from "../types";

function ConditionNodeComponent({ id, data, selected }: NodeProps<ConditionNodeData>) {
  return (
    <div
      className={`builder-node builder-node--condition ${selected ? "builder-node--selected" : ""}`}
      onClick={() => data.onSelect(id)}
    >
      <Handle type="target" position={Position.Left} className="builder-handle" />

      <div className="builder-node__header">
        <div className="builder-node__icon bg-info">IF</div>
        <div className="builder-node__info">
          <div className="builder-node__label">{data.label}</div>
          <div className="builder-node__type">Condition</div>
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
        <div className="builder-node__condition">
          <code className="text-[10px]">{data.condition || "{{ condition }}"}</code>
        </div>
      </div>

      {/* Two output handles for true/false branches */}
      <div className="builder-node__outputs">
        <div className="builder-node__output builder-node__output--true">
          <span className="builder-node__output-label">True</span>
          <Handle
            type="source"
            position={Position.Right}
            id="true"
            className="builder-handle builder-handle--true"
            style={{ top: "30%" }}
          />
        </div>
        <div className="builder-node__output builder-node__output--false">
          <span className="builder-node__output-label">False</span>
          <Handle
            type="source"
            position={Position.Right}
            id="false"
            className="builder-handle builder-handle--false"
            style={{ top: "70%" }}
          />
        </div>
      </div>
    </div>
  );
}

export const ConditionNode = memo(ConditionNodeComponent);
