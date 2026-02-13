import { useState } from "react";
import { Copy, CheckCircle } from "lucide-react";
import { Card } from "../ui/Card";
import { Button } from "../ui/Button";
import { useStatus, type GatewayStatus } from "../../hooks/useStatus";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatUptime(seconds?: number): string {
  if (seconds == null) return "N/A";
  if (seconds < 60) return `${seconds}s`;
  const mins = Math.floor(seconds / 60);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  const remainMins = mins % 60;
  if (hrs < 24) return remainMins > 0 ? `${hrs}h ${remainMins}m` : `${hrs}h`;
  const days = Math.floor(hrs / 24);
  const remainHrs = hrs % 24;
  return remainHrs > 0 ? `${days}d ${remainHrs}h` : `${days}d`;
}

interface InfoRow {
  label: string;
  value: string;
  mono?: boolean;
}

function extractInfoRows(status: GatewayStatus): InfoRow[] {
  return [
    {
      label: "Gateway Version",
      value: status.build?.version ?? "N/A",
    },
    {
      label: "Build Commit",
      value: status.build?.commit ? status.build.commit.slice(0, 7) : "N/A",
      mono: true,
    },
    {
      label: "Build Date",
      value: status.build?.date ?? "N/A",
    },
    {
      label: "Uptime",
      value: formatUptime(status.uptime_seconds),
    },
    {
      label: "Server Time",
      value: status.time
        ? new Date(status.time).toLocaleString()
        : "N/A",
    },
    {
      label: "Workers Connected",
      value: String(status.workers?.count ?? 0),
    },
    {
      label: "NATS Status",
      value: status.nats?.connected ? "Connected" : "Disconnected",
    },
    {
      label: "Redis Status",
      value: status.redis?.ok ? "OK" : (status.redis?.error ?? "Unavailable"),
    },
  ];
}

// ---------------------------------------------------------------------------
// SystemInfoSection
// ---------------------------------------------------------------------------

export function SystemInfoSection() {
  const { data: status, isLoading } = useStatus();
  const [copied, setCopied] = useState(false);

  if (isLoading || !status) {
    return (
      <Card className="animate-pulse">
        <div className="space-y-2">
          {Array.from({ length: 6 }, (_, i) => (
            <div key={i} className="flex justify-between">
              <div className="h-3 w-24 rounded bg-surface2" />
              <div className="h-3 w-32 rounded bg-surface2" />
            </div>
          ))}
        </div>
      </Card>
    );
  }

  const rows = extractInfoRows(status);

  function handleCopy() {
    const text = rows.map((r) => `${r.label}: ${r.value}`).join("\n");
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }

  return (
    <Card>
      <div className="flex items-center justify-between mb-3">
        <p className="text-xs text-muted">
          System information from the gateway status endpoint.
        </p>
        <Button variant="ghost" size="sm" onClick={handleCopy}>
          {copied ? (
            <><CheckCircle className="mr-1 h-3 w-3 text-success" /> Copied</>
          ) : (
            <><Copy className="mr-1 h-3 w-3" /> Copy</>
          )}
        </Button>
      </div>

      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        {rows.map((row) => (
          <div key={row.label} className="flex justify-between text-xs">
            <span className="text-muted">{row.label}</span>
            <span className={row.mono ? "font-mono text-ink" : "text-ink"}>
              {row.value}
            </span>
          </div>
        ))}
      </div>
    </Card>
  );
}
