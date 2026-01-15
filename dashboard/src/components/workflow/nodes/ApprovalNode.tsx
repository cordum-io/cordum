import { memo } from "react";
import { Handle, Position, type NodeProps } from "reactflow";
import type { ApprovalNodeData } from "../types";

function ApprovalNodeComponent({ id, data, selected }: NodeProps<ApprovalNodeData>) {
  return (
    <div
      className={`builder-node builder-node--approval ${selected ? "builder-node--selected" : ""}`}
      onClick={() => data.onSelect(id)}
    >
      <Handle type="target" position={Position.Left} className="builder-handle" />

      <div className="builder-node__header">
        <div className="builder-node__icon bg-warning">AP</div>
        <div className="builder-node__info">
          <div className="builder-node__label">{data.label}</div>
          <div className="builder-node__type">Approval Gate</div>
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
        {data.approverRole && (
          <div className="builder-node__field">
            <span className="builder-node__field-label">Role:</span>
            <span className="builder-node__field-value">{data.approverRole}</span>
          </div>
        )}
        {data.approvalPolicy && (
          <div className="builder-node__field">
            <span className="builder-node__field-label">Policy:</span>
            <span className="builder-node__field-value">{data.approvalPolicy}</span>
          </div>
        )}
        {!data.approverRole && !data.approvalPolicy && (
          <div className="builder-node__empty">
            Requires manual approval
          </div>
        )}
      </div>

      <Handle type="source" position={Position.Right} id="approved" className="builder-handle" />
    </div>
  );
}

export const ApprovalNode = memo(ApprovalNodeComponent);
