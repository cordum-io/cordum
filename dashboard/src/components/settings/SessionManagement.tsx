import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Select } from "../ui/Select";
import { useAuthConfigAdmin, useSetConfig } from "../../hooks/useSettings";

// ---------------------------------------------------------------------------
// TTL options
// ---------------------------------------------------------------------------

const TTL_OPTIONS = [
  { value: "1h", label: "1 hour" },
  { value: "4h", label: "4 hours" },
  { value: "8h", label: "8 hours" },
  { value: "24h", label: "24 hours" },
  { value: "7d", label: "7 days" },
  { value: "30d", label: "30 days" },
] as const;

// ---------------------------------------------------------------------------
// SessionManagement
// ---------------------------------------------------------------------------

export function SessionManagement() {
  const { data: authConfig, isLoading } = useAuthConfigAdmin();
  const setConfig = useSetConfig();

  const currentTtl = authConfig?.session_ttl ?? "24h";

  function handleTtlChange(ttl: string) {
    setConfig.mutate({ auth: { session_ttl: ttl } });
  }

  if (isLoading) {
    return (
      <Card className="animate-pulse">
        <div className="space-y-3">
          <div className="h-4 w-48 rounded bg-surface2" />
          <div className="h-8 w-32 rounded bg-surface2" />
        </div>
      </Card>
    );
  }

  return (
    <div className="space-y-4">
      {/* Session TTL */}
      <Card>
        <div className="flex items-center justify-between">
          <div>
            <h3 className="text-sm font-semibold text-ink">Session Timeout</h3>
            <p className="mt-0.5 text-xs text-muted-foreground">
              How long user sessions remain active before requiring re-authentication.
            </p>
          </div>
          <Select
            className="w-32"
            value={currentTtl}
            onChange={(e) => handleTtlChange(e.target.value)}
            disabled={setConfig.isPending}
          >
            {TTL_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </Select>
        </div>
      </Card>

      {/* Active sessions (placeholder) */}
      <Card>
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-sm font-semibold text-ink">Active Sessions</h3>
          <Badge variant="default">Preview</Badge>
        </div>

        <div className="overflow-x-auto rounded-xl border border-border">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-border bg-surface2/50">
                <th className="px-3 py-2 text-left font-medium text-muted-foreground">User</th>
                <th className="px-3 py-2 text-left font-medium text-muted-foreground">IP</th>
                <th className="px-3 py-2 text-left font-medium text-muted-foreground">Device</th>
                <th className="px-3 py-2 text-left font-medium text-muted-foreground">Last Active</th>
                <th className="px-3 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y divide-border text-muted-foreground">
              <tr>
                <td colSpan={5} className="px-3 py-8 text-center">
                  <p className="text-sm text-muted-foreground">
                    Session listing requires backend v2.x
                  </p>
                  <p className="mt-1 text-[10px] text-muted-foreground">
                    Active session management will be available when the sessions API endpoint is deployed.
                  </p>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div className="mt-3 flex justify-end">
          <Button
            variant="danger"
            size="sm"
            disabled
            title="Requires sessions API endpoint"
          >
            Force Logout All
          </Button>
        </div>
      </Card>
    </div>
  );
}
