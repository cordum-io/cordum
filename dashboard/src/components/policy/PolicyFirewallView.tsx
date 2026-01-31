import { useMemo, useState } from "react";
import { ArrowDown, ArrowUp, Copy, Plus, Trash2 } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Drawer } from "../ui/Drawer";
import { Input } from "../ui/Input";
import { Select } from "../ui/Select";
import { Textarea } from "../ui/Textarea";
import { decisionTypeMeta } from "../../lib/status";
import {
  parsePolicyBundle,
  updateBundleRules,
  type PolicyConstraintsDraft,
  type PolicyMatchDraft,
  type PolicyRemediationDraft,
  type PolicyRuleDraft,
} from "../../lib/policy-bundle";

type PolicyFirewallViewProps = {
  bundleId: string;
  content: string;
  editable: boolean;
  sourceLabel: string;
  highlightRuleId?: string;
  onChangeContent: (next: string) => void;
  onRequestRaw: () => void;
};

const decisionOptions = [
  { value: "allow", label: "Allow" },
  { value: "deny", label: "Deny" },
  { value: "require_approval", label: "Require approval" },
  { value: "allow_with_constraints", label: "Allow with constraints" },
  { value: "throttle", label: "Throttle" },
];

const emptyMatch = (): PolicyMatchDraft => ({});
const emptyConstraints = (): PolicyConstraintsDraft => ({});

const toCsv = (items?: string[]) => (items && items.length ? items.join(", ") : "");
const fromCsv = (value: string) =>
  value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);

const labelsToText = (labels?: Record<string, string>) => {
  if (!labels) return "";
  return Object.entries(labels)
    .map(([key, value]) => `${key}=${value}`)
    .join("\n");
};

const textToLabels = (value: string) => {
  const out: Record<string, string> = {};
  value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .forEach((line) => {
      const [key, ...rest] = line.split("=");
      const k = key?.trim();
      const v = rest.join("=").trim();
      if (k && v) {
        out[k] = v;
      }
    });
  return Object.keys(out).length ? out : undefined;
};

const redactEmpty = (value?: string) => (value && value.trim() ? value.trim() : undefined);

const ensureRuleId = (rule: PolicyRuleDraft) => {
  if (rule.id && rule.id.trim()) {
    return rule.id.trim();
  }
  return `rule-${Date.now()}`;
};

const decisionBadge = (decision?: string) => {
  const meta = decisionTypeMeta(decision);
  const toneToVariant: Record<string, "success" | "warning" | "danger" | "info" | "default"> = {
    success: "success",
    warning: "warning",
    danger: "danger",
    info: "info",
    muted: "default",
    accent: "info",
  };
  return { label: meta.label, variant: toneToVariant[meta.tone] || "default" };
};

function matchChips(match?: PolicyMatchDraft) {
  if (!match) return [];
  const chips: string[] = [];
  if (match.topics?.length) chips.push(`Topics: ${match.topics.slice(0, 3).join(", ")}${match.topics.length > 3 ? "…" : ""}`);
  if (match.tenants?.length) chips.push(`Tenants: ${match.tenants.slice(0, 3).join(", ")}${match.tenants.length > 3 ? "…" : ""}`);
  if (match.capabilities?.length)
    chips.push(`Capabilities: ${match.capabilities.slice(0, 3).join(", ")}${match.capabilities.length > 3 ? "…" : ""}`);
  if (match.pack_ids?.length) chips.push(`Packs: ${match.pack_ids.slice(0, 3).join(", ")}${match.pack_ids.length > 3 ? "…" : ""}`);
  if (match.risk_tags?.length) chips.push(`Risk: ${match.risk_tags.slice(0, 3).join(", ")}${match.risk_tags.length > 3 ? "…" : ""}`);
  if (match.requires?.length) chips.push(`Requires: ${match.requires.slice(0, 3).join(", ")}${match.requires.length > 3 ? "…" : ""}`);
  if (match.actor_types?.length) chips.push(`Actors: ${match.actor_types.slice(0, 3).join(", ")}${match.actor_types.length > 3 ? "…" : ""}`);
  if (match.actor_ids?.length) chips.push(`Actor IDs: ${match.actor_ids.slice(0, 2).join(", ")}${match.actor_ids.length > 2 ? "…" : ""}`);
  if (match.secrets_present !== undefined) chips.push(`Secrets: ${match.secrets_present ? "present" : "absent"}`);
  if (match.labels && Object.keys(match.labels).length) chips.push("Labels");
  if (match.mcp && Object.keys(match.mcp).length) chips.push("MCP");
  return chips;
}

function constraintsChips(constraints?: PolicyConstraintsDraft) {
  if (!constraints) return [];
  const chips: string[] = [];
  if (constraints.budgets?.max_runtime_ms !== undefined) chips.push(`Runtime ≤ ${constraints.budgets.max_runtime_ms}ms`);
  if (constraints.budgets?.max_retries !== undefined) chips.push(`Retries ≤ ${constraints.budgets.max_retries}`);
  if (constraints.budgets?.max_artifact_bytes !== undefined) chips.push(`Artifacts ≤ ${constraints.budgets.max_artifact_bytes}B`);
  if (constraints.budgets?.max_concurrent_jobs !== undefined) chips.push(`Concurrency ≤ ${constraints.budgets.max_concurrent_jobs}`);
  if (constraints.sandbox?.isolated !== undefined) chips.push(constraints.sandbox.isolated ? "Sandboxed" : "No sandbox");
  if (constraints.sandbox?.network_allowlist?.length) chips.push("Net allowlist");
  if (constraints.sandbox?.fs_read_only?.length) chips.push("FS read-only");
  if (constraints.sandbox?.fs_read_write?.length) chips.push("FS read-write");
  if (constraints.toolchain?.allowed_tools?.length) chips.push("Tool allowlist");
  if (constraints.toolchain?.allowed_commands?.length) chips.push("Command allowlist");
  if (constraints.diff?.max_files !== undefined) chips.push(`Diff files ≤ ${constraints.diff.max_files}`);
  if (constraints.diff?.max_lines !== undefined) chips.push(`Diff lines ≤ ${constraints.diff.max_lines}`);
  if (constraints.diff?.deny_path_globs?.length) chips.push("Diff denylist");
  if (constraints.redaction_level) chips.push(`Redaction: ${constraints.redaction_level}`);
  return chips;
}

export function PolicyFirewallView({
  bundleId,
  content,
  editable,
  sourceLabel,
  highlightRuleId,
  onChangeContent,
  onRequestRaw,
}: PolicyFirewallViewProps) {
  const parsed = useMemo(() => parsePolicyBundle(content), [content]);
  const rules = parsed.rules;
  const [draft, setDraft] = useState<PolicyRuleDraft | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);

  const setRules = (nextRules: PolicyRuleDraft[]) => {
    const next = updateBundleRules(parsed.root, nextRules);
    onChangeContent(next);
  };

  const closeDrawer = () => {
    setDrawerOpen(false);
    setDraft(null);
  };

  const openNewRule = () => {
    setDraft({
      uid: `draft-${Date.now()}`,
      id: "",
      decision: "allow",
      reason: "",
      match: emptyMatch(),
      constraints: emptyConstraints(),
      remediations: [],
    });
    setDrawerOpen(true);
  };

  const openEditRule = (rule: PolicyRuleDraft) => {
    setDraft({
      ...rule,
      match: rule.match ? { ...rule.match } : emptyMatch(),
      constraints: rule.constraints ? { ...rule.constraints } : emptyConstraints(),
      remediations: rule.remediations ? rule.remediations.map((item) => ({ ...item })) : [],
    });
    setDrawerOpen(true);
  };

  const saveRule = () => {
    if (!draft) return;
    const nextId = ensureRuleId(draft);
    const nextDraft: PolicyRuleDraft = {
      ...draft,
      id: nextId,
      decision: redactEmpty(draft.decision),
      reason: redactEmpty(draft.reason),
      match: draft.match,
      constraints: draft.constraints,
      remediations: draft.remediations?.filter((item) => Object.values(item).some((value) => value !== undefined && value !== "")),
    };

    const existingIndex = rules.findIndex((rule) => rule.uid === draft.uid);
    const nextRules = [...rules];
    if (existingIndex >= 0) {
      nextRules[existingIndex] = nextDraft;
    } else {
      nextRules.push(nextDraft);
    }
    setRules(nextRules);
    closeDrawer();
  };

  const deleteRule = (rule: PolicyRuleDraft) => {
    const nextRules = rules.filter((item) => item.uid !== rule.uid);
    setRules(nextRules);
  };

  const duplicateRule = (rule: PolicyRuleDraft) => {
    const clone: PolicyRuleDraft = {
      ...rule,
      uid: `dup-${Date.now()}`,
      id: rule.id ? `${rule.id}-copy` : `rule-${Date.now()}`,
    };
    setRules([...rules, clone]);
  };

  const moveRule = (index: number, direction: -1 | 1) => {
    const target = index + direction;
    if (target < 0 || target >= rules.length) return;
    const next = [...rules];
    const [removed] = next.splice(index, 1);
    next.splice(target, 0, removed);
    setRules(next);
  };

  if (parsed.error) {
    return (
      <div className="rounded-2xl border border-border bg-white/70 p-4 text-sm text-danger">
        <div className="font-semibold">Firewall view unavailable</div>
        <div className="mt-2 text-xs text-muted">{parsed.error}</div>
        <Button variant="outline" size="sm" type="button" className="mt-3" onClick={onRequestRaw}>
          Open Raw editor
        </Button>
      </div>
    );
  }

  if (parsed.hasLegacyTenants && !rules.length) {
    return (
      <div className="rounded-2xl border border-border bg-white/70 p-4 text-sm text-muted">
        <div className="font-semibold text-ink">Legacy tenant policies detected.</div>
        <div className="mt-1">Switch to Raw editor to update tenant allow/deny lists.</div>
        <Button variant="outline" size="sm" type="button" className="mt-3" onClick={onRequestRaw}>
          Open Raw editor
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="text-xs text-muted">
          Bundle {bundleId} · Source {sourceLabel} · {rules.length} rules
        </div>
        {editable ? (
          <Button variant="primary" size="sm" type="button" onClick={openNewRule}>
            <Plus className="h-4 w-4" />
            Add rule
          </Button>
        ) : null}
      </div>

      {rules.length === 0 ? (
        <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
          No rules defined yet.
          {editable ? (
            <Button variant="outline" size="sm" type="button" className="mt-3" onClick={openNewRule}>
              Create first rule
            </Button>
          ) : null}
        </div>
      ) : (
        <div className="space-y-3">
          {rules.map((rule, index) => {
            const matchSummary = matchChips(rule.match);
            const constraintSummary = constraintsChips(rule.constraints);
            const decision = decisionBadge(rule.decision);
            const isHighlighted = highlightRuleId && rule.id === highlightRuleId;
            return (
              <div
                key={rule.uid}
                className={`rounded-2xl border border-border bg-white/70 p-4 ${isHighlighted ? "ring-2 ring-accent" : ""}`}
              >
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div className="flex items-center gap-3">
                    <div className="text-xs font-semibold text-muted">#{index + 1}</div>
                    <Badge variant={decision.variant}>{decision.label}</Badge>
                    <div className="text-sm font-semibold text-ink">{rule.id || "Untitled rule"}</div>
                  </div>
                  {editable ? (
                    <div className="flex flex-wrap items-center gap-2">
                      <Button
                        variant="outline"
                        size="sm"
                        type="button"
                        onClick={() => moveRule(index, -1)}
                        disabled={index === 0}
                      >
                        <ArrowUp className="h-4 w-4" />
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        type="button"
                        onClick={() => moveRule(index, 1)}
                        disabled={index === rules.length - 1}
                      >
                        <ArrowDown className="h-4 w-4" />
                      </Button>
                      <Button variant="outline" size="sm" type="button" onClick={() => duplicateRule(rule)}>
                        <Copy className="h-4 w-4" />
                      </Button>
                      <Button variant="outline" size="sm" type="button" onClick={() => openEditRule(rule)}>
                        Edit
                      </Button>
                      <Button variant="danger" size="sm" type="button" onClick={() => deleteRule(rule)}>
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                  ) : null}
                </div>
                {rule.reason ? <div className="mt-2 text-xs text-muted">{rule.reason}</div> : null}
                <div className="mt-3 flex flex-wrap gap-2">
                  {(matchSummary.length ? matchSummary : ["Any"]).map((item) => (
                    <span key={item} className="rounded-full border border-border bg-white/80 px-3 py-1 text-[10px] text-ink">
                      {item}
                    </span>
                  ))}
                </div>
                {constraintSummary.length ? (
                  <div className="mt-2 flex flex-wrap gap-2">
                    {constraintSummary.map((item) => (
                      <span key={item} className="rounded-full border border-border bg-white/80 px-3 py-1 text-[10px] text-ink">
                        {item}
                      </span>
                    ))}
                  </div>
                ) : null}
              </div>
            );
          })}
        </div>
      )}

      <Drawer open={drawerOpen} onClose={closeDrawer}>
        {draft ? (
          <div className="space-y-5">
            <div>
              <div className="text-xs uppercase tracking-[0.2em] text-muted">Rule metadata</div>
              <div className="mt-3 grid gap-3 lg:grid-cols-2">
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Rule ID</label>
                  <Input
                    value={draft.id || ""}
                    onChange={(event) => setDraft((prev) => (prev ? { ...prev, id: event.target.value } : prev))}
                    placeholder="rule-allow-prod"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Decision</label>
                  <Select
                    value={draft.decision || "allow"}
                    onChange={(event) =>
                      setDraft((prev) => (prev ? { ...prev, decision: event.target.value } : prev))
                    }
                  >
                    {decisionOptions.map((option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </Select>
                </div>
                <div className="lg:col-span-2">
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Reason</label>
                  <Input
                    value={draft.reason || ""}
                    onChange={(event) => setDraft((prev) => (prev ? { ...prev, reason: event.target.value } : prev))}
                    placeholder="Short human explanation"
                  />
                </div>
              </div>
            </div>

            <div>
              <div className="text-xs uppercase tracking-[0.2em] text-muted">Match</div>
              <div className="mt-3 grid gap-3 lg:grid-cols-2">
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Topics</label>
                  <Input
                    value={toCsv(draft.match?.topics)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? { ...prev, match: { ...prev.match, topics: fromCsv(event.target.value) } }
                          : prev
                      )
                    }
                    placeholder="job.prod.*, job.secops.*"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Tenants</label>
                  <Input
                    value={toCsv(draft.match?.tenants)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? { ...prev, match: { ...prev.match, tenants: fromCsv(event.target.value) } }
                          : prev
                      )
                    }
                    placeholder="default, sandbox"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Capabilities</label>
                  <Input
                    value={toCsv(draft.match?.capabilities)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? { ...prev, match: { ...prev.match, capabilities: fromCsv(event.target.value) } }
                          : prev
                      )
                    }
                    placeholder="db.read, repo.write"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Pack IDs</label>
                  <Input
                    value={toCsv(draft.match?.pack_ids)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? { ...prev, match: { ...prev.match, pack_ids: fromCsv(event.target.value) } }
                          : prev
                      )
                    }
                    placeholder="packs/db"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Risk tags</label>
                  <Input
                    value={toCsv(draft.match?.risk_tags)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? { ...prev, match: { ...prev.match, risk_tags: fromCsv(event.target.value) } }
                          : prev
                      )
                    }
                    placeholder="pci, pii"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Requires</label>
                  <Input
                    value={toCsv(draft.match?.requires)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? { ...prev, match: { ...prev.match, requires: fromCsv(event.target.value) } }
                          : prev
                      )
                    }
                    placeholder="approval, escrow"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Actor types</label>
                  <Input
                    value={toCsv(draft.match?.actor_types)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? { ...prev, match: { ...prev.match, actor_types: fromCsv(event.target.value) } }
                          : prev
                      )
                    }
                    placeholder="service, human"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Actor IDs</label>
                  <Input
                    value={toCsv(draft.match?.actor_ids)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? { ...prev, match: { ...prev.match, actor_ids: fromCsv(event.target.value) } }
                          : prev
                      )
                    }
                    placeholder="user-123"
                  />
                </div>
                <div className="lg:col-span-2">
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Labels (key=value)</label>
                  <Textarea
                    rows={3}
                    value={labelsToText(draft.match?.labels)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? { ...prev, match: { ...prev.match, labels: textToLabels(event.target.value) } }
                          : prev
                      )
                    }
                    placeholder="env=prod\nteam=payments"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Secrets present</label>
                  <Select
                    value={
                      draft.match?.secrets_present === undefined
                        ? ""
                        : draft.match?.secrets_present
                        ? "true"
                        : "false"
                    }
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              match: {
                                ...prev.match,
                                secrets_present: event.target.value
                                  ? event.target.value === "true"
                                  : undefined,
                              },
                            }
                          : prev
                      )
                    }
                  >
                    <option value="">Any</option>
                    <option value="true">Present</option>
                    <option value="false">Absent</option>
                  </Select>
                </div>
              </div>
            </div>

            <div>
              <div className="text-xs uppercase tracking-[0.2em] text-muted">Constraints</div>
              <div className="mt-3 grid gap-3 lg:grid-cols-2">
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Max runtime (ms)</label>
                  <Input
                    type="number"
                    value={draft.constraints?.budgets?.max_runtime_ms ?? ""}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              constraints: {
                                ...prev.constraints,
                                budgets: {
                                  ...prev.constraints?.budgets,
                                  max_runtime_ms: event.target.value ? Number(event.target.value) : undefined,
                                },
                              },
                            }
                          : prev
                      )
                    }
                    placeholder="120000"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Max retries</label>
                  <Input
                    type="number"
                    value={draft.constraints?.budgets?.max_retries ?? ""}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              constraints: {
                                ...prev.constraints,
                                budgets: {
                                  ...prev.constraints?.budgets,
                                  max_retries: event.target.value ? Number(event.target.value) : undefined,
                                },
                              },
                            }
                          : prev
                      )
                    }
                    placeholder="3"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Max artifact bytes</label>
                  <Input
                    type="number"
                    value={draft.constraints?.budgets?.max_artifact_bytes ?? ""}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              constraints: {
                                ...prev.constraints,
                                budgets: {
                                  ...prev.constraints?.budgets,
                                  max_artifact_bytes: event.target.value ? Number(event.target.value) : undefined,
                                },
                              },
                            }
                          : prev
                      )
                    }
                    placeholder="10485760"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Max concurrent jobs</label>
                  <Input
                    type="number"
                    value={draft.constraints?.budgets?.max_concurrent_jobs ?? ""}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              constraints: {
                                ...prev.constraints,
                                budgets: {
                                  ...prev.constraints?.budgets,
                                  max_concurrent_jobs: event.target.value ? Number(event.target.value) : undefined,
                                },
                              },
                            }
                          : prev
                      )
                    }
                    placeholder="5"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Sandbox isolated</label>
                  <Select
                    value={
                      draft.constraints?.sandbox?.isolated === undefined
                        ? ""
                        : draft.constraints?.sandbox?.isolated
                        ? "true"
                        : "false"
                    }
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              constraints: {
                                ...prev.constraints,
                                sandbox: {
                                  ...prev.constraints?.sandbox,
                                  isolated: event.target.value ? event.target.value === "true" : undefined,
                                },
                              },
                            }
                          : prev
                      )
                    }
                  >
                    <option value="">Any</option>
                    <option value="true">Isolated</option>
                    <option value="false">Not isolated</option>
                  </Select>
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Network allowlist</label>
                  <Input
                    value={toCsv(draft.constraints?.sandbox?.network_allowlist)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              constraints: {
                                ...prev.constraints,
                                sandbox: {
                                  ...prev.constraints?.sandbox,
                                  network_allowlist: fromCsv(event.target.value),
                                },
                              },
                            }
                          : prev
                      )
                    }
                    placeholder="api.example.com"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">FS read-only</label>
                  <Input
                    value={toCsv(draft.constraints?.sandbox?.fs_read_only)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              constraints: {
                                ...prev.constraints,
                                sandbox: {
                                  ...prev.constraints?.sandbox,
                                  fs_read_only: fromCsv(event.target.value),
                                },
                              },
                            }
                          : prev
                      )
                    }
                    placeholder="/etc, /usr"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">FS read-write</label>
                  <Input
                    value={toCsv(draft.constraints?.sandbox?.fs_read_write)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              constraints: {
                                ...prev.constraints,
                                sandbox: {
                                  ...prev.constraints?.sandbox,
                                  fs_read_write: fromCsv(event.target.value),
                                },
                              },
                            }
                          : prev
                      )
                    }
                    placeholder="/tmp, /var/tmp"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Allowed tools</label>
                  <Input
                    value={toCsv(draft.constraints?.toolchain?.allowed_tools)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              constraints: {
                                ...prev.constraints,
                                toolchain: {
                                  ...prev.constraints?.toolchain,
                                  allowed_tools: fromCsv(event.target.value),
                                },
                              },
                            }
                          : prev
                      )
                    }
                    placeholder="git, curl"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Allowed commands</label>
                  <Input
                    value={toCsv(draft.constraints?.toolchain?.allowed_commands)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              constraints: {
                                ...prev.constraints,
                                toolchain: {
                                  ...prev.constraints?.toolchain,
                                  allowed_commands: fromCsv(event.target.value),
                                },
                              },
                            }
                          : prev
                      )
                    }
                    placeholder="ls, cat"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Max diff files</label>
                  <Input
                    type="number"
                    value={draft.constraints?.diff?.max_files ?? ""}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              constraints: {
                                ...prev.constraints,
                                diff: {
                                  ...prev.constraints?.diff,
                                  max_files: event.target.value ? Number(event.target.value) : undefined,
                                },
                              },
                            }
                          : prev
                      )
                    }
                    placeholder="5"
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Max diff lines</label>
                  <Input
                    type="number"
                    value={draft.constraints?.diff?.max_lines ?? ""}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              constraints: {
                                ...prev.constraints,
                                diff: {
                                  ...prev.constraints?.diff,
                                  max_lines: event.target.value ? Number(event.target.value) : undefined,
                                },
                              },
                            }
                          : prev
                      )
                    }
                    placeholder="300"
                  />
                </div>
                <div className="lg:col-span-2">
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Diff deny globs</label>
                  <Input
                    value={toCsv(draft.constraints?.diff?.deny_path_globs)}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              constraints: {
                                ...prev.constraints,
                                diff: {
                                  ...prev.constraints?.diff,
                                  deny_path_globs: fromCsv(event.target.value),
                                },
                              },
                            }
                          : prev
                      )
                    }
                    placeholder="**/*.env"
                  />
                </div>
                <div className="lg:col-span-2">
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Redaction level</label>
                  <Input
                    value={draft.constraints?.redaction_level ?? ""}
                    onChange={(event) =>
                      setDraft((prev) =>
                        prev
                          ? {
                              ...prev,
                              constraints: {
                                ...prev.constraints,
                                redaction_level: redactEmpty(event.target.value),
                              },
                            }
                          : prev
                      )
                    }
                    placeholder="standard"
                  />
                </div>
              </div>
            </div>

            <div>
              <div className="text-xs uppercase tracking-[0.2em] text-muted">Remediations</div>
              <div className="mt-3 space-y-3">
                {(draft.remediations || []).length === 0 ? (
                  <div className="text-xs text-muted">No remediations attached.</div>
                ) : null}
                {(draft.remediations || []).map((remediation, idx) => (
                  <div key={`${remediation.id || "remediation"}-${idx}`} className="rounded-2xl border border-border bg-white/70 p-3">
                    <div className="flex items-center justify-between">
                      <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Remediation {idx + 1}</div>
                      <Button
                        variant="outline"
                        size="sm"
                        type="button"
                        onClick={() =>
                          setDraft((prev) =>
                            prev
                              ? {
                                  ...prev,
                                  remediations: (prev.remediations || []).filter((_, rIdx) => rIdx !== idx),
                                }
                              : prev
                          )
                        }
                      >
                        Remove
                      </Button>
                    </div>
                    <div className="mt-3 grid gap-3 lg:grid-cols-2">
                      <div>
                        <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">ID</label>
                        <Input
                          value={remediation.id || ""}
                          onChange={(event) =>
                            setDraft((prev) => {
                              if (!prev) return prev;
                              const next = [...(prev.remediations || [])];
                              next[idx] = { ...next[idx], id: event.target.value };
                              return { ...prev, remediations: next };
                            })
                          }
                        />
                      </div>
                      <div>
                        <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Title</label>
                        <Input
                          value={remediation.title || ""}
                          onChange={(event) =>
                            setDraft((prev) => {
                              if (!prev) return prev;
                              const next = [...(prev.remediations || [])];
                              next[idx] = { ...next[idx], title: event.target.value };
                              return { ...prev, remediations: next };
                            })
                          }
                        />
                      </div>
                      <div className="lg:col-span-2">
                        <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Summary</label>
                        <Input
                          value={remediation.summary || ""}
                          onChange={(event) =>
                            setDraft((prev) => {
                              if (!prev) return prev;
                              const next = [...(prev.remediations || [])];
                              next[idx] = { ...next[idx], summary: event.target.value };
                              return { ...prev, remediations: next };
                            })
                          }
                        />
                      </div>
                      <div>
                        <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Replacement topic</label>
                        <Input
                          value={remediation.replacement_topic || ""}
                          onChange={(event) =>
                            setDraft((prev) => {
                              if (!prev) return prev;
                              const next = [...(prev.remediations || [])];
                              next[idx] = { ...next[idx], replacement_topic: event.target.value };
                              return { ...prev, remediations: next };
                            })
                          }
                        />
                      </div>
                      <div>
                        <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Replacement capability</label>
                        <Input
                          value={remediation.replacement_capability || ""}
                          onChange={(event) =>
                            setDraft((prev) => {
                              if (!prev) return prev;
                              const next = [...(prev.remediations || [])];
                              next[idx] = { ...next[idx], replacement_capability: event.target.value };
                              return { ...prev, remediations: next };
                            })
                          }
                        />
                      </div>
                      <div className="lg:col-span-2">
                        <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Add labels (key=value)</label>
                        <Textarea
                          rows={2}
                          value={labelsToText(remediation.add_labels)}
                          onChange={(event) =>
                            setDraft((prev) => {
                              if (!prev) return prev;
                              const next = [...(prev.remediations || [])];
                              next[idx] = { ...next[idx], add_labels: textToLabels(event.target.value) };
                              return { ...prev, remediations: next };
                            })
                          }
                        />
                      </div>
                      <div className="lg:col-span-2">
                        <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Remove labels</label>
                        <Input
                          value={toCsv(remediation.remove_labels)}
                          onChange={(event) =>
                            setDraft((prev) => {
                              if (!prev) return prev;
                              const next = [...(prev.remediations || [])];
                              next[idx] = { ...next[idx], remove_labels: fromCsv(event.target.value) };
                              return { ...prev, remediations: next };
                            })
                          }
                        />
                      </div>
                    </div>
                  </div>
                ))}
                <Button
                  variant="outline"
                  size="sm"
                  type="button"
                  onClick={() =>
                    setDraft((prev) => {
                      if (!prev) return prev;
                      const next = [...(prev.remediations || [])];
                      const item: PolicyRemediationDraft = {
                        id: "",
                        title: "",
                        summary: "",
                        replacement_topic: "",
                        replacement_capability: "",
                        add_labels: undefined,
                        remove_labels: [],
                      };
                      next.push(item);
                      return { ...prev, remediations: next };
                    })
                  }
                >
                  Add remediation
                </Button>
              </div>
            </div>

            <div className="flex items-center justify-end gap-2">
              <Button variant="outline" type="button" onClick={closeDrawer}>
                Cancel
              </Button>
              <Button variant="primary" type="button" onClick={saveRule}>
                Save rule
              </Button>
            </div>
          </div>
        ) : null}
      </Drawer>
    </div>
  );
}
