interface TenantLimitsSectionProps {
  maxConcurrentJobs?: number;
  allowedRepoHosts: string[];
  deniedRepoHosts: string[];
}

function HostTag({
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
          ? "rounded bg-[var(--color-success)]/20 px-2 py-0.5 text-xs font-mono text-[var(--color-success)]"
          : "rounded bg-destructive/20 px-2 py-0.5 text-xs font-mono text-destructive"
      }
    >
      {value}
    </span>
  );
}

export function TenantLimitsSection({
  maxConcurrentJobs,
  allowedRepoHosts,
  deniedRepoHosts,
}: TenantLimitsSectionProps) {
  return (
    <section className="rounded-xl border border-border bg-surface-0 p-4 space-y-3">
      <div>
        <h3 className="font-display text-sm font-semibold text-foreground">Limits</h3>
        <p className="mt-1 text-xs text-muted-foreground">
          Tenant-level limits are guardrails layered on top of global policy defaults.
        </p>
      </div>

      <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
        <div className="rounded border border-border/70 bg-surface-1 p-3">
          <p className="text-xs font-mono uppercase tracking-wider text-muted-foreground">
            max_concurrent_jobs
          </p>
          <p className="mt-2 text-xl font-mono font-semibold text-foreground">
            {typeof maxConcurrentJobs === "number" ? maxConcurrentJobs : "—"}
          </p>
        </div>

        <div className="rounded border border-border/70 bg-surface-1 p-3 md:col-span-2">
          <p className="mb-2 text-xs font-mono uppercase tracking-wider text-muted-foreground">
            repo host boundaries
          </p>
          <div className="space-y-2">
            <div>
              <p className="mb-1 text-xs text-muted-foreground">allowed_repo_hosts</p>
              <div className="flex flex-wrap gap-1.5">
                {allowedRepoHosts.length === 0 && (
                  <span className="text-xs text-muted-foreground">No allow host overrides configured.</span>
                )}
                {allowedRepoHosts.map((host) => (
                  <HostTag key={`allow-host-${host}`} tone="allow" value={host} />
                ))}
              </div>
            </div>
            <div>
              <p className="mb-1 text-xs text-muted-foreground">denied_repo_hosts</p>
              <div className="flex flex-wrap gap-1.5">
                {deniedRepoHosts.length === 0 && (
                  <span className="text-xs text-muted-foreground">No deny host overrides configured.</span>
                )}
                {deniedRepoHosts.map((host) => (
                  <HostTag key={`deny-host-${host}`} tone="deny" value={host} />
                ))}
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

