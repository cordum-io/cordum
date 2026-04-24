interface TenantTopicAccessSectionProps {
  allowTopics: string[];
  denyTopics: string[];
}

function TopicTag({
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

export function TenantTopicAccessSection({
  allowTopics,
  denyTopics,
}: TenantTopicAccessSectionProps) {
  return (
    <section className="rounded-xl border border-border bg-surface-0 p-4 space-y-3">
      <div>
        <h3 className="font-display text-sm font-semibold text-foreground">
          Topic Access Control
        </h3>
        <p className="mt-1 text-xs text-muted-foreground">
          Tenant topic policy is evaluated as pattern matching where{" "}
          <span className="font-medium text-foreground">deny overrides allow</span>.
          Keep deny patterns specific to avoid unintended broad blocks.
        </p>
        <p className="mt-1 text-xs text-muted-foreground">
          Patterns are treated as case-insensitive globs by policy evaluators.
        </p>
      </div>

      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <div className="rounded border border-border/70 bg-surface-1 p-3">
          <p className="mb-2 text-xs font-mono uppercase tracking-wider text-muted-foreground">
            allow_topics
          </p>
          <div className="flex flex-wrap gap-1.5">
            {allowTopics.length === 0 && (
              <span className="text-xs text-muted-foreground">No allow patterns configured.</span>
            )}
            {allowTopics.map((topic) => (
              <TopicTag key={`allow-${topic}`} tone="allow" value={topic} />
            ))}
          </div>
        </div>

        <div className="rounded border border-border/70 bg-surface-1 p-3">
          <p className="mb-2 text-xs font-mono uppercase tracking-wider text-muted-foreground">
            deny_topics
          </p>
          <div className="flex flex-wrap gap-1.5">
            {denyTopics.length === 0 && (
              <span className="text-xs text-muted-foreground">No deny patterns configured.</span>
            )}
            {denyTopics.map((topic) => (
              <TopicTag key={`deny-${topic}`} tone="deny" value={topic} />
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}

