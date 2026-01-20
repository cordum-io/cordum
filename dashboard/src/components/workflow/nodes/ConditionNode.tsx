import { memo } from "react";
import { Handle, Position, type NodeProps } from "reactflow";
import type { ConditionNodeData } from "../types";
import { NodeStatus } from "./NodeStatus";

function ConditionNodeComponent({ id, data, selected }: NodeProps<ConditionNodeData>) {
  const isReadOnly = Boolean(data.readOnly);
  return (
    <div
      className={`builder-node builder-node--condition ${selected ? "builder-node--selected" : ""}`}
      onClick={() => {
        if (!isReadOnly) {
          data.onSelect(id);
        }
      }}
    >
      <Handle type="target" position={Position.Left} className="builder-handle" />

      <div className="builder-node__header">
        <div className="builder-node__icon bg-info">IF</div>
        <div className="builder-node__info">
          <div className="builder-node__label">{data.label}</div>
          <div className="builder-node__type">Condition</div>
        </div>
        {!isReadOnly ? (
          <button
            onClick={(e) => {
              e.stopPropagation();
              data.onDelete(id);
            }}
            className="builder-node__delete"
          >
            &times;
          </button>
        ) : null}
      </div>

      <div className="builder-node__body">
        <div className="builder-node__condition">
          <code className="text-[10px]">{data.condition || "{{ condition }}"}</code>
        </div>
      </div>

      {isReadOnly ? <NodeStatus status={data.status} /> : null}
      <Handle type="source" position={Position.Right} id="output" className="builder-handle" />
    </div>
  );
}

export const ConditionNode = memo(ConditionNodeComponent);
