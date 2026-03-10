import { Globe, Settings, ArrowUpRight, Clock } from "lucide-react";
import { Card } from "../ui/Card";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { cn } from "../../lib/utils";
import type { Environment } from "../../api/types";

function timeAgo(iso?: string): string {
  if (!iso) return "Never";
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diff / 60_000);
  if (mins < 1) return "Just now";
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

const STATUS_VARIANT: Record<string, "success" | "warning" | "danger"> = {
  active: "success",
  maintenance: "warning",
  degraded: "danger",
};

const STATUS_BORDER: Record<string, string> = {
  active: "border-l-success",
  maintenance: "border-l-warning",
  degraded: "border-l-danger",
};

interface EnvironmentCardProps {
  env: Environment;
  onEdit: (env: Environment) => void;
  onPromote: (env: Environment) => void;
  isOnly: boolean;
}

export function EnvironmentCard({ env, onEdit, onPromote, isOnly }: EnvironmentCardProps) {
  const configCount = Object.keys(env.config ?? {}).length;

  return (
    <Card className={cn("border-l-4", STATUS_BORDER[env.status] ?? "border-l-border")}>
      <div className="flex items-start justify-between">
        <div className="space-y-1">
          <h3 className="font-display text-base font-semibold text-ink capitalize">{env.name}</h3>
          <Badge variant={STATUS_VARIANT[env.status] ?? "default"}>{env.status}</Badge>
        </div>
        <Globe className="h-5 w-5 text-muted-foreground opacity-40" />
      </div>

      <div className="mt-4 space-y-2 text-xs text-muted-foreground">
        {env.endpoint ? (
          <div className="flex items-center gap-2">
            <Globe className="h-3.5 w-3.5 shrink-0" />
            <span className="truncate font-mono">{env.endpoint}</span>
          </div>
        ) : (
          <div className="flex items-center gap-2">
            <Globe className="h-3.5 w-3.5 shrink-0" />
            <span className="italic">No endpoint configured</span>
          </div>
        )}

        <div className="flex items-center gap-2">
          <Clock className="h-3.5 w-3.5 shrink-0" />
          <span>Deployed {timeAgo(env.lastDeployedAt)}</span>
        </div>

        <div className="flex items-center gap-2">
          <Settings className="h-3.5 w-3.5 shrink-0" />
          <span>{configCount} config {configCount === 1 ? "key" : "keys"}</span>
        </div>
      </div>

      <div className="mt-4 flex items-center gap-2">
        <Button variant="outline" size="sm" type="button" onClick={() => onEdit(env)}>
          <Settings className="h-3.5 w-3.5" />
          Edit Config
        </Button>
        <Button
          variant="ghost"
          size="sm"
          type="button"
          onClick={() => onPromote(env)}
          disabled={isOnly || env.name === "production"}
        >
          <ArrowUpRight className="h-3.5 w-3.5" />
          Promote
        </Button>
      </div>
    </Card>
  );
}
