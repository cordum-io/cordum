export interface TenantMcpMatrix {
  allowServers: string[];
  denyServers: string[];
  allowTools: string[];
  denyTools: string[];
  allowResources: string[];
  denyResources: string[];
  allowActions: string[];
  denyActions: string[];
}

interface TenantMcpGovernanceSectionProps {
  matrix: TenantMcpMatrix;
}

function McpTag({
  value,
  tone,
}: {
  value: string;
  tone: "allow" | "deny";
}) {
  return (
    <span
      className={
        tone === "allow"
          ? "rounded bg-[var(--color-info)]/20 px-2 py-0.5 text-[10px] font-mono text-[var(--color-info)]"
          : "rounded bg-destructive/20 px-2 py-0.5 text-[10px] font-mono text-destructive"
      }
    >
      {value}
    </span>
  );
}

function McpField({
  title,
  allowValues,
  denyValues,
}: {
  title: string;
  allowValues: string[];
  denyValues: string[];
}) {
  return (
    <div className="rounded border border-border/70 bg-surface-1 p-3">
      <p className="mb-2 text-[10px] font-mono uppercase tracking-wider text-muted-foreground">
        {title}
      </p>
      <div className="space-y-2">
        <div>
          <p className="mb-1 text-[10px] text-muted-foreground">allow</p>
          <div className="flex flex-wrap gap-1.5">
            {allowValues.length === 0 && (
              <span className="text-xs text-muted-foreground">No explicit allow entries.</span>
            )}
            {allowValues.map((value) => (
              <McpTag key={`allow-${title}-${value}`} tone="allow" value={value} />
            ))}
          </div>
        </div>
        <div>
          <p className="mb-1 text-[10px] text-muted-foreground">deny</p>
          <div className="flex flex-wrap gap-1.5">
            {denyValues.length === 0 && (
              <span className="text-xs text-muted-foreground">No explicit deny entries.</span>
            )}
            {denyValues.map((value) => (
              <McpTag key={`deny-${title}-${value}`} tone="deny" value={value} />
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

export function TenantMcpGovernanceSection({
  matrix,
}: TenantMcpGovernanceSectionProps) {
  return (
    <section className="rounded-lg border border-border bg-surface-0 p-4 space-y-3">
      <div>
        <h3 className="font-display text-sm font-semibold text-foreground">
          MCP Governance
        </h3>
        <p className="mt-1 text-xs text-muted-foreground">
          Tenant page is the canonical home for MCP allow/deny governance.
          Use this section to define server/tool/resource/action boundaries for this tenant.
        </p>
        <p className="mt-1 text-[11px] text-muted-foreground">
          Precedence rule: <span className="font-medium text-foreground">deny overrides allow</span> when both patterns match.
        </p>
      </div>

      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <McpField
          title="servers"
          allowValues={matrix.allowServers}
          denyValues={matrix.denyServers}
        />
        <McpField
          title="tools"
          allowValues={matrix.allowTools}
          denyValues={matrix.denyTools}
        />
        <McpField
          title="resources"
          allowValues={matrix.allowResources}
          denyValues={matrix.denyResources}
        />
        <McpField
          title="actions"
          allowValues={matrix.allowActions}
          denyValues={matrix.denyActions}
        />
      </div>
    </section>
  );
}
