import { Badge } from "../ui/Badge";
import { errorCodeLabel, errorCodeCategory } from "../../api/types";

const categoryVariant: Record<string, "danger" | "warning" | "info" | "default"> = {
  safety: "danger",
  job: "warning",
  protocol: "info",
  transport: "default",
  unknown: "default",
};

interface ErrorCodeBadgeProps {
  errorCodeEnum?: number;
  errorCode?: string;
}

export function ErrorCodeBadge({ errorCodeEnum, errorCode }: ErrorCodeBadgeProps) {
  if (errorCodeEnum && errorCodeEnum !== 0) {
    const category = errorCodeCategory(errorCodeEnum);
    return (
      <Badge variant={categoryVariant[category]}>
        {errorCodeLabel(errorCodeEnum)}
      </Badge>
    );
  }

  if (errorCode) {
    return <Badge variant="default">{errorCode}</Badge>;
  }

  return null;
}
