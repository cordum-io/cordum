import type { ActivityItem } from "../../types/activity";
import { MessageBlock } from "./MessageBlock";
import { ThoughtBlock } from "./ThoughtBlock";
import { ToolCallBlock } from "./ToolCallBlock";
import { ToolResultBlock } from "./ToolResultBlock";
import { SafetyAlertBlock } from "./SafetyAlertBlock";
import { StateChangeBlock } from "./StateChangeBlock";
import { ContextUpdateBlock } from "./ContextUpdateBlock";

type Props = {
  activity: ActivityItem;
  onApprove?: (jobId: string) => void;
  onReject?: (jobId: string) => void;
};

export function ActivityBlock({ activity, onApprove, onReject }: Props) {
  switch (activity.type) {
    case "thought":
      return <ThoughtBlock activity={activity} />;
    case "tool_call":
      return <ToolCallBlock activity={activity} />;
    case "tool_result":
      return <ToolResultBlock activity={activity} />;
    case "safety_event":
      return <SafetyAlertBlock activity={activity} onApprove={onApprove} onReject={onReject} />;
    case "state_change":
      return <StateChangeBlock activity={activity} />;
    case "context_update":
      return <ContextUpdateBlock activity={activity} />;
    case "message":
    default:
      return <MessageBlock activity={activity} />;
  }
}
