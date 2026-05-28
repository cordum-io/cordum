/*
 * AgentIdentityCreateForm — inline form rendered inside the AgentIdentityPanel's
 * empty state for a worker that's heartbeating but has no Agent Identity yet.
 *
 * Context (GitHub #314): the panel used to show "no identity" with no call to
 * action. Operators had to leave the page, find the /agents catalog, and create
 * one manually — meanwhile every audit row for the worker fell back to the raw
 * agent_id instead of a human-readable agent_label. This form pre-fills from
 * the worker's latest heartbeat so the common case is one button + Save.
 */
import { useState, type FormEvent } from "react";
import { Button } from "@/components/ui/Button";
import type { Worker } from "@/api/types";
import { useCreateAgentIdentity, type CreateAgentIdentityBody } from "@/hooks/useAgentIdentities";

interface AgentIdentityCreateFormProps {
  /** The agent_id we're registering — comes from the URL / heartbeat record. */
  agentId: string;
  /** Latest heartbeat snapshot if the worker is online; used for pre-fill. */
  heartbeat?: Worker;
  /** Default owner for the form — usually the logged-in operator's principalId. */
  defaultOwner?: string;
  /** Called when the create succeeds; the panel reacts by re-fetching. */
  onCreated?: () => void;
  /** Optional cancel handler to collapse the form back to the empty-state CTA. */
  onCancel?: () => void;
}

/** Build a sensible initial body from heartbeat data. Falls back gracefully
 *  when fields are missing — the user can edit anything before submit. */
function initialBody(
  agentId: string,
  heartbeat: Worker | undefined,
  defaultOwner: string | undefined,
): CreateAgentIdentityBody {
  const name = heartbeat?.name?.trim() || prettify(agentId);
  return {
    // agent_id links the new identity to this heartbeating worker so the
    // panel resolves it and audit rows stop falling back to the raw id (#314).
    agent_id: agentId,
    name,
    owner: defaultOwner?.trim() || "",
    risk_tier: "low",
    description: heartbeat?.type
      ? `Auto-registered from worker heartbeat (type=${heartbeat.type}, pool=${heartbeat.pool || "?"})`
      : "Auto-registered from worker heartbeat.",
    allowed_topics: [], // server-side: rules govern routing; leave empty by default
    allowed_pools: heartbeat?.pool ? [heartbeat.pool] : [],
    data_classifications: ["internal"],
  };
}

/** "support-triage-bot" → "Support Triage Bot" — a stable, deterministic prettifier
 *  for the common case where worker_id is the only thing we have. */
function prettify(id: string): string {
  return id
    .split(/[-_.]/g)
    .filter(Boolean)
    .map((seg) => seg.charAt(0).toUpperCase() + seg.slice(1))
    .join(" ");
}

export default function AgentIdentityCreateForm({
  agentId,
  heartbeat,
  defaultOwner,
  onCreated,
  onCancel,
}: AgentIdentityCreateFormProps) {
  const [body, setBody] = useState<CreateAgentIdentityBody>(
    () => initialBody(agentId, heartbeat, defaultOwner),
  );
  const mutation = useCreateAgentIdentity();

  function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (mutation.isPending) return;
    mutation.mutate(body, {
      onSuccess: () => onCreated?.(),
    });
  }

  function update<K extends keyof CreateAgentIdentityBody>(
    key: K,
    value: CreateAgentIdentityBody[K],
  ) {
    setBody((prev) => ({ ...prev, [key]: value }));
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4 text-left max-w-xl mx-auto">
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <label className="block">
          <span className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
            Display name
          </span>
          <input
            required
            value={body.name}
            onChange={(e) => update("name", e.target.value)}
            placeholder="e.g. Support Triage Bot"
            className="mt-1 w-full rounded-lg border border-border bg-surface-0 px-3 py-2 text-sm font-mono text-foreground focus:outline-none focus:ring-2 focus:ring-cordum"
          />
        </label>
        <label className="block">
          <span className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
            Owner
          </span>
          <input
            required
            value={body.owner}
            onChange={(e) => update("owner", e.target.value)}
            placeholder="operator id"
            className="mt-1 w-full rounded-lg border border-border bg-surface-0 px-3 py-2 text-sm font-mono text-foreground focus:outline-none focus:ring-2 focus:ring-cordum"
          />
        </label>
        <label className="block">
          <span className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
            Risk tier
          </span>
          <select
            value={body.risk_tier}
            onChange={(e) => update("risk_tier", e.target.value)}
            className="mt-1 w-full rounded-lg border border-border bg-surface-0 px-3 py-2 text-sm font-mono text-foreground focus:outline-none focus:ring-2 focus:ring-cordum"
          >
            <option value="low">low</option>
            <option value="medium">medium</option>
            <option value="high">high</option>
            <option value="critical">critical</option>
          </select>
        </label>
        <label className="block">
          <span className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
            Team (optional)
          </span>
          <input
            value={body.team ?? ""}
            onChange={(e) => update("team", e.target.value || undefined)}
            placeholder="e.g. platform-ops"
            className="mt-1 w-full rounded-lg border border-border bg-surface-0 px-3 py-2 text-sm font-mono text-foreground focus:outline-none focus:ring-2 focus:ring-cordum"
          />
        </label>
      </div>
      <label className="block">
        <span className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
          Description
        </span>
        <textarea
          rows={2}
          value={body.description ?? ""}
          onChange={(e) => update("description", e.target.value || undefined)}
          className="mt-1 w-full rounded-lg border border-border bg-surface-0 px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-cordum"
        />
      </label>
      {heartbeat?.pool && (
        <div className="text-xs text-muted-foreground">
          Pre-filled <span className="font-mono">allowed_pools=[{heartbeat.pool}]</span> from heartbeat.
          Edit via the catalog after creation if you need finer scope.
        </div>
      )}
      {mutation.isError && (
        <div className="text-xs text-destructive border border-destructive/30 bg-destructive/10 rounded-lg px-3 py-2">
          {mutation.error?.message ?? "Failed to create agent identity."}
        </div>
      )}
      <div className="flex items-center gap-2">
        <Button type="submit" variant="default" size="sm" disabled={mutation.isPending}>
          {mutation.isPending ? "Creating…" : "Create identity"}
        </Button>
        {onCancel && (
          <Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={mutation.isPending}>
            Cancel
          </Button>
        )}
      </div>
    </form>
  );
}
