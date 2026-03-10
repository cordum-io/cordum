import { Link } from "react-router-dom";
import { Database, Gauge, Radio, Server, ExternalLink, Info } from "lucide-react";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { Badge } from "../ui/Badge";
import type { GatewayStatus } from "../../hooks/useStatus";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface HAConfigSectionProps {
  status?: GatewayStatus;
}

// ---------------------------------------------------------------------------
// Read-only field row
// ---------------------------------------------------------------------------

function EnvField({
  label,
  envVar,
  value,
  fallback,
}: {
  label: string;
  envVar: string;
  value: string;
  fallback: string;
}) {
  const display = value || fallback;
  const isDefault = !value;

  return (
    <div className="flex items-center justify-between py-2">
      <div className="space-y-0.5">
        <p className="text-sm text-ink">{label}</p>
        <code className="text-[11px] text-muted-foreground">{envVar}</code>
      </div>
      <div className="flex items-center gap-2">
        <span className="font-mono text-sm text-ink">{display}</span>
        {isDefault && (
          <Badge variant="default">default</Badge>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Section header
// ---------------------------------------------------------------------------

function SectionIcon({
  icon: Icon,
  title,
}: {
  icon: typeof Database;
  title: string;
}) {
  return (
    <div className="flex items-center gap-2 border-b border-border pb-2 mb-1">
      <Icon className="h-4 w-4 text-accent" />
      <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        {title}
      </span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// HAConfigSection
// ---------------------------------------------------------------------------

export function HAConfigSection({ status }: HAConfigSectionProps) {
  const haEnv = status?.ha_env;
  const rateLimiterMode = status?.rate_limiter?.mode;
  const instanceId = status?.instance_id;

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between">
          <CardTitle className="text-sm">HA Configuration</CardTitle>
          <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
            <Info className="h-3.5 w-3.5" />
            Requires restart to change
          </div>
        </div>
      </CardHeader>

      <div className="space-y-5">
        {/* System */}
        <div>
          <SectionIcon icon={Server} title="System" />
          <EnvField
            label="Instance ID"
            envVar="CORDUM_INSTANCE_ID"
            value={instanceId ?? ""}
            fallback="auto-generated"
          />
        </div>

        {/* Rate Limiting */}
        <div>
          <SectionIcon icon={Gauge} title="Rate Limiting" />
          <EnvField
            label="Rate limit backend"
            envVar="REDIS_RATE_LIMIT"
            value={rateLimiterMode === "redis" ? "true (distributed)" : rateLimiterMode === "memory" ? "false (local)" : ""}
            fallback="local"
          />
        </div>

        {/* Redis */}
        <div>
          <SectionIcon icon={Database} title="Redis" />
          <div className="divide-y divide-border/50">
            <EnvField
              label="Pool size"
              envVar="REDIS_POOL_SIZE"
              value={haEnv?.redis_pool_size ?? ""}
              fallback="20"
            />
            <EnvField
              label="Min idle connections"
              envVar="REDIS_MIN_IDLE_CONNS"
              value={haEnv?.redis_min_idle_conns ?? ""}
              fallback="5"
            />
          </div>
        </div>

        {/* Audit */}
        <div>
          <SectionIcon icon={Radio} title="Audit" />
          <EnvField
            label="Transport"
            envVar="AUDIT_TRANSPORT"
            value={haEnv?.audit_transport ?? ""}
            fallback="buffer"
          />
        </div>

        {/* MCP — cross-reference */}
        <div className="rounded-lg border border-border/50 bg-surface2/30 px-3 py-2">
          <div className="flex items-center justify-between">
            <div className="space-y-0.5">
              <p className="text-sm text-ink">MCP Transport &amp; Address</p>
              <p className="text-[11px] text-muted-foreground">
                MCP_TRANSPORT, MCP_HTTP_ADDR
              </p>
            </div>
            <Link
              to="/settings/mcp"
              className="flex items-center gap-1 text-xs font-medium text-accent hover:underline"
            >
              MCP Settings
              <ExternalLink className="h-3 w-3" />
            </Link>
          </div>
        </div>
      </div>
    </Card>
  );
}
