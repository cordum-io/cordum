import { useEffect, useMemo, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { z } from "zod";
import { useSearchParams } from "react-router-dom";
import { api } from "../lib/api";
import { formatRelative } from "../lib/format";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Badge } from "../components/ui/Badge";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Textarea } from "../components/ui/Textarea";
import { Drawer } from "../components/ui/Drawer";
import { ApprovalStatusBadge, JobStatusBadge } from "../components/StatusBadge";
import type { ApprovalItem, PolicyRule } from "../types/api";

const schema = z.object({
  topic: z.string().min(1, "Topic required"),
  tenantId: z.string().optional(),
  workflowId: z.string().optional(),
  stepId: z.string().optional(),
  capability: z.string().optional(),
  packId: z.string().optional(),
  riskTags: z.string().optional(),
  requires: z.string().optional(),
  actorId: z.string().optional(),
  actorType: z.string().optional(),
  priority: z.string().optional(),
  estimatedCost: z.string().optional(),
});

type FormValues = z.infer<typeof schema>;

type DiffLine = {
  left: string;
  right: string;
  match: boolean;
};

function buildLineDiff(left: string, right: string): DiffLine[] {
  const leftLines = left.split("\n");
  const rightLines = right.split("\n");
  const max = Math.max(leftLines.length, rightLines.length);
  const out: DiffLine[] = [];
  for (let i = 0; i < max; i += 1) {
    const l = leftLines[i] ?? "";
    const r = rightLines[i] ?? "";
    out.push({ left: l, right: r, match: l === r });
  }
  return out;
}

function decisionBadgeMeta(decision?: string): { label: string; variant: "success" | "warning" | "danger" | "info" | "default" } {
  const normalized = (decision || "").toUpperCase();
  if (!normalized) {
    return { label: "UNKNOWN", variant: "default" };
  }
  if (normalized.includes("DENY")) {
    return { label: normalized.replace("DECISION_TYPE_", ""), variant: "danger" };
  }
  if (normalized.includes("REQUIRE")) {
    return { label: normalized.replace("DECISION_TYPE_", ""), variant: "warning" };
  }
  if (normalized.includes("ALLOW_WITH")) {
    return { label: normalized.replace("DECISION_TYPE_", ""), variant: "info" };
  }
  if (normalized.includes("ALLOW")) {
    return { label: normalized.replace("DECISION_TYPE_", ""), variant: "success" };
  }
  return { label: normalized.replace("DECISION_TYPE_", ""), variant: "default" };
}

function isSafeApproval(item: ApprovalItem): boolean {
  const decision = (item.decision || "").toUpperCase();
  if (decision.includes("DENY") || decision.includes("THROTTLE")) {
    return false;
  }
  const hasRiskTags = (item.job.risk_tags || []).length > 0;
  const hasRequires = (item.job.requires || []).length > 0;
  const constraints = item.constraints as Record<string, unknown> | undefined;
  const hasConstraints = constraints ? Object.keys(constraints).length > 0 : false;
  return !hasRiskTags && !hasRequires && !hasConstraints;
}

export function PolicyPage() {
  const queryClient = useQueryClient();
  const [searchParams] = useSearchParams();
  const approvalsQuery = useInfiniteQuery({
    queryKey: ["approvals"],
    queryFn: ({ pageParam }) => api.listApprovals(100, pageParam as number | undefined),
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    initialPageParam: undefined as number | undefined,
  });
  const snapshotsQuery = useQuery({
    queryKey: ["policy", "snapshots"],
    queryFn: () => api.listPolicySnapshots(),
  });
  const policyBundlesQuery = useQuery({
    queryKey: ["policy", "bundles"],
    queryFn: () => api.getPolicyBundles(),
  });
  const policyRulesQuery = useQuery({
    queryKey: ["policy", "rules"],
    queryFn: () => api.listPolicyRules(),
  });
  const policyBundleSnapshotsQuery = useQuery({
    queryKey: ["policy", "bundle-snapshots"],
    queryFn: () => api.listPolicyBundleSnapshots(),
  });

  const approveMutation = useMutation({
    mutationFn: (payload: { jobId: string; reason?: string; note?: string }) =>
      api.approveJob(payload.jobId, { reason: payload.reason, note: payload.note }),
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({ queryKey: ["approvals"] });
      setSelectedIds((prev) => {
        const next = new Set(prev);
        next.delete(variables.jobId);
        return next;
      });
    },
  });
  const rejectMutation = useMutation({
    mutationFn: (payload: { jobId: string; reason?: string; note?: string }) =>
      api.rejectJob(payload.jobId, { reason: payload.reason, note: payload.note }),
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({ queryKey: ["approvals"] });
      setSelectedIds((prev) => {
        const next = new Set(prev);
        next.delete(variables.jobId);
        return next;
      });
    },
  });
  const bulkApproveMutation = useMutation({
    mutationFn: (payload: { ids: string[]; reason?: string; note?: string }) =>
      Promise.all(payload.ids.map((id) => api.approveJob(id, { reason: payload.reason, note: payload.note }))),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["approvals"] });
      setSelectedIds(new Set());
    },
  });
  const bulkRejectMutation = useMutation({
    mutationFn: (payload: { ids: string[]; reason?: string; note?: string }) =>
      Promise.all(payload.ids.map((id) => api.rejectJob(id, { reason: payload.reason, note: payload.note }))),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["approvals"] });
      setSelectedIds(new Set());
    },
  });
  const captureSnapshotMutation = useMutation({
    mutationFn: (note?: string) => api.capturePolicyBundleSnapshot({ note }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["policy", "bundle-snapshots"] });
      queryClient.invalidateQueries({ queryKey: ["policy", "bundles"] });
      setSnapshotNote("");
    },
  });

  const [mode, setMode] = useState<"simulate" | "explain" | "evaluate">("simulate");
  const [response, setResponse] = useState<Record<string, unknown> | null>(null);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [bulkReason, setBulkReason] = useState("");
  const [bulkNote, setBulkNote] = useState("");
  const [selectedApproval, setSelectedApproval] = useState<ApprovalItem | null>(null);
  const [compareText, setCompareText] = useState("");
  const [selectedSnapshotId, setSelectedSnapshotId] = useState("");
  const [snapshotNote, setSnapshotNote] = useState("");
  const [showSafeOnly, setShowSafeOnly] = useState(false);

  const snapshotDetailQuery = useQuery({
    queryKey: ["policy", "bundle-snapshot", selectedSnapshotId],
    queryFn: () => api.getPolicyBundleSnapshot(selectedSnapshotId),
    enabled: Boolean(selectedSnapshotId),
  });

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      topic: "job.default",
      tenantId: "default",
      actorType: "service",
    },
  });

  const policyMutation = useMutation({
    mutationFn: async (values: FormValues) => {
      const request = {
        topic: values.topic,
        tenant: values.tenantId || "default",
        workflow_id: values.workflowId,
        step_id: values.stepId,
        priority: values.priority,
        estimated_cost: values.estimatedCost ? Number(values.estimatedCost) : undefined,
        meta: {
          tenant_id: values.tenantId || "default",
          actor_id: values.actorId,
          actor_type: values.actorType,
          capability: values.capability,
          pack_id: values.packId,
          risk_tags: values.riskTags ? values.riskTags.split(",").map((tag) => tag.trim()).filter(Boolean) : [],
          requires: values.requires ? values.requires.split(",").map((tag) => tag.trim()).filter(Boolean) : [],
        },
      };
      if (mode === "simulate") {
        return api.policySimulate(request);
      }
      if (mode === "explain") {
        return api.policyExplain(request);
      }
      return api.policyEvaluate(request);
    },
    onSuccess: (data) => setResponse(data as Record<string, unknown>),
  });

  const kernelSnapshots = useMemo(() => {
    const data = snapshotsQuery.data as { snapshots?: string[] } | undefined;
    return data?.snapshots ?? [];
  }, [snapshotsQuery.data]);

  const bundleSnapshots = useMemo(
    () => policyBundleSnapshotsQuery.data?.items ?? [],
    [policyBundleSnapshotsQuery.data]
  );
  const policyRules = useMemo(() => policyRulesQuery.data?.items ?? [], [policyRulesQuery.data]);
  const policyRuleErrors = useMemo(() => policyRulesQuery.data?.errors ?? [], [policyRulesQuery.data]);

  const currentBundlesText = useMemo(() => {
    const bundles = (policyBundlesQuery.data?.bundles || {}) as Record<string, unknown>;
    return JSON.stringify(bundles, null, 2);
  }, [policyBundlesQuery.data]);

  const diffLines = useMemo(() => {
    if (!compareText.trim()) {
      return [];
    }
    return buildLineDiff(currentBundlesText, compareText);
  }, [compareText, currentBundlesText]);

  useEffect(() => {
    if (!snapshotDetailQuery.data?.bundles) {
      return;
    }
    const next = JSON.stringify(snapshotDetailQuery.data.bundles, null, 2);
    setCompareText(next);
  }, [snapshotDetailQuery.data]);

  useEffect(() => {
    setSelectedIds(new Set());
  }, [showSafeOnly]);

  const approvals = approvalsQuery.data?.pages.flatMap((page) => page.items) ?? [];
  const safeApprovals = useMemo(() => approvals.filter((item) => isSafeApproval(item)), [approvals]);
  const visibleApprovals = showSafeOnly ? safeApprovals : approvals;
  const selectedCount = selectedIds.size;
  const allSelected = visibleApprovals.length > 0 && visibleApprovals.every((item) => selectedIds.has(item.job.id));

  const jobIdParam = searchParams.get("job_id") || "";
  const topicParam = searchParams.get("topic") || "";
  const tenantParam = searchParams.get("tenant") || "";
  const workflowParam = searchParams.get("workflow_id") || "";
  const stepParam = searchParams.get("step_id") || "";
  const capabilityParam = searchParams.get("capability") || "";
  const packParam = searchParams.get("pack_id") || "";
  const actorIdParam = searchParams.get("actor_id") || "";
  const actorTypeParam = searchParams.get("actor_type") || "";
  const riskTagsParam = searchParams.get("risk_tags") || "";
  const requiresParam = searchParams.get("requires") || "";

  useEffect(() => {
    if (!jobIdParam) {
      return;
    }
    const match = approvals.find((item) => item.job.id === jobIdParam);
    if (match) {
      setSelectedApproval(match);
    }
  }, [jobIdParam, approvals]);

  useEffect(() => {
    if (
      !topicParam &&
      !tenantParam &&
      !workflowParam &&
      !stepParam &&
      !capabilityParam &&
      !packParam &&
      !actorIdParam &&
      !actorTypeParam &&
      !riskTagsParam &&
      !requiresParam
    ) {
      return;
    }
    reset({
      topic: topicParam || "job.default",
      tenantId: tenantParam || "default",
      workflowId: workflowParam || undefined,
      stepId: stepParam || undefined,
      capability: capabilityParam || undefined,
      packId: packParam || undefined,
      actorId: actorIdParam || undefined,
      actorType: actorTypeParam || "service",
      riskTags: riskTagsParam || undefined,
      requires: requiresParam || undefined,
      priority: undefined,
      estimatedCost: undefined,
    });
  }, [
    actorIdParam,
    actorTypeParam,
    capabilityParam,
    packParam,
    requiresParam,
    reset,
    riskTagsParam,
    stepParam,
    tenantParam,
    topicParam,
    workflowParam,
  ]);

  const toggleSelected = (jobId: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(jobId)) {
        next.delete(jobId);
      } else {
        next.add(jobId);
      }
      return next;
    });
  };

  const setAllSelected = (checked: boolean) => {
    if (!checked) {
      setSelectedIds(new Set());
      return;
    }
    setSelectedIds(new Set(visibleApprovals.map((item) => item.job.id)));
  };

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Approvals Inbox</CardTitle>
          <div className="text-xs text-muted">Pending approval-required jobs</div>
        </CardHeader>
        {visibleApprovals.length ? (
          <div className="space-y-4">
            <div className="rounded-2xl border border-border bg-white/70 p-4">
              <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
                <label className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted">
                  <input
                    type="checkbox"
                    checked={allSelected}
                    onChange={(event) => setAllSelected(event.target.checked)}
                  />
                  Select all
                </label>
                <label className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted">
                  <input
                    type="checkbox"
                    checked={showSafeOnly}
                    onChange={(event) => setShowSafeOnly(event.target.checked)}
                  />
                  Only safe
                </label>
                <div className="flex flex-1 flex-col gap-2 lg:flex-row lg:items-center">
                  <Input
                    value={bulkReason}
                    onChange={(event) => setBulkReason(event.target.value)}
                    placeholder="Reason for decision (optional)"
                  />
                  <Input value={bulkNote} onChange={(event) => setBulkNote(event.target.value)} placeholder="Note (optional)" />
                </div>
                <div className="flex flex-wrap gap-2">
                  <Button
                    variant="primary"
                    size="sm"
                    type="button"
                    onClick={() =>
                      bulkApproveMutation.mutate({
                        ids: Array.from(selectedIds),
                        reason: bulkReason || undefined,
                        note: bulkNote || undefined,
                      })
                    }
                    disabled={selectedCount === 0 || bulkApproveMutation.isPending || bulkRejectMutation.isPending}
                  >
                    Approve {selectedCount || ""}
                  </Button>
                  <Button
                    variant="subtle"
                    size="sm"
                    type="button"
                    onClick={() =>
                      bulkApproveMutation.mutate({
                        ids: safeApprovals.map((item) => item.job.id),
                        reason: bulkReason || undefined,
                        note: bulkNote || undefined,
                      })
                    }
                    disabled={safeApprovals.length === 0 || bulkApproveMutation.isPending || bulkRejectMutation.isPending}
                  >
                    Approve all safe {safeApprovals.length ? `(${safeApprovals.length})` : ""}
                  </Button>
                  <Button
                    variant="danger"
                    size="sm"
                    type="button"
                    onClick={() =>
                      bulkRejectMutation.mutate({
                        ids: Array.from(selectedIds),
                        reason: bulkReason || undefined,
                        note: bulkNote || undefined,
                      })
                    }
                    disabled={selectedCount === 0 || bulkApproveMutation.isPending || bulkRejectMutation.isPending}
                  >
                    Reject {selectedCount || ""}
                  </Button>
                </div>
              </div>
              <div className="mt-2 text-xs text-muted">
                Reason and note apply to all selected approvals.
              </div>
            </div>
            <div className="space-y-3">
              {visibleApprovals.map((item) => {
                const decision = decisionBadgeMeta(item.decision);
                const safe = isSafeApproval(item);
                return (
                  <div key={item.job.id} className="list-row">
                    <div className="grid gap-3 lg:grid-cols-[auto_minmax(0,1fr)_auto] lg:items-center">
                      <div className="flex items-center gap-2">
                        <input
                          type="checkbox"
                          checked={selectedIds.has(item.job.id)}
                          onChange={() => toggleSelected(item.job.id)}
                        />
                        <div>
                          <div className="text-sm font-semibold text-ink">Job {item.job.id.slice(0, 8)}</div>
                          <div className="text-xs text-muted">Topic {item.job.topic || "-"}</div>
                        </div>
                      </div>
                      <div className="text-xs text-muted">
                        Tenant {item.job.tenant || "default"}
                        {item.job.pack_id ? ` · Pack ${item.job.pack_id}` : ""}
                        {item.policy_reason ? ` · ${item.policy_reason}` : ""}
                      </div>
                      <div className="flex flex-wrap items-center gap-2 justify-end">
                        {safe ? <Badge variant="success">SAFE</Badge> : null}
                        <Badge variant={decision.variant}>{decision.label}</Badge>
                        <ApprovalStatusBadge required={item.approval_required} />
                        <JobStatusBadge state={item.job.state} />
                      </div>
                    </div>
                    <div className="mt-3 flex flex-wrap gap-2">
                      <Button
                        variant="outline"
                        size="sm"
                        type="button"
                        onClick={() => setSelectedApproval(item)}
                      >
                        Details
                      </Button>
                      <Button
                        variant="primary"
                        size="sm"
                        type="button"
                        onClick={() =>
                          approveMutation.mutate({
                            jobId: item.job.id,
                            reason: bulkReason || undefined,
                            note: bulkNote || undefined,
                          })
                        }
                        disabled={approveMutation.isPending || bulkApproveMutation.isPending || bulkRejectMutation.isPending}
                      >
                        Approve
                      </Button>
                      <Button
                        variant="danger"
                        size="sm"
                        type="button"
                        onClick={() =>
                          rejectMutation.mutate({
                            jobId: item.job.id,
                            reason: bulkReason || undefined,
                            note: bulkNote || undefined,
                          })
                        }
                        disabled={approveMutation.isPending || bulkApproveMutation.isPending || bulkRejectMutation.isPending}
                      >
                        Reject
                      </Button>
                    </div>
                  </div>
                );
              })}
            </div>
            {approvalsQuery.hasNextPage ? (
              <div className="pt-2">
                <Button
                  variant="outline"
                  size="sm"
                  type="button"
                  onClick={() => approvalsQuery.fetchNextPage()}
                  disabled={approvalsQuery.isFetchingNextPage}
                >
                  {approvalsQuery.isFetchingNextPage ? "Loading..." : "Load more"}
                </Button>
              </div>
            ) : null}
          </div>
        ) : approvalsQuery.isLoading ? (
          <div className="text-sm text-muted">Loading approvals...</div>
        ) : (
          <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
            {showSafeOnly ? "No approvals match the safe filter." : "No approvals waiting."}
          </div>
        )}
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Policy Rules</CardTitle>
          <div className="text-xs text-muted">Evaluated bundle rules and legacy tenant policies</div>
        </CardHeader>
        {policyRulesQuery.isLoading ? (
          <div className="text-sm text-muted">Loading policy rules...</div>
        ) : policyRules.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
            No policy rules found. Add rules to policy bundles to populate this list.
          </div>
        ) : (
          <div className="space-y-3">
            {policyRules.map((rule, index) => {
              const decision = decisionBadgeMeta(rule.decision as string | undefined);
              const source = rule.source as PolicyRule["source"] | undefined;
              const sourceLabel = source?.pack_id
                ? `${source.pack_id}${source.overlay_name ? ` / ${source.overlay_name}` : ""}`
                : source?.fragment_id || "unknown";
              return (
                <div key={`${rule.id || "rule"}-${index}`} className="rounded-2xl border border-border bg-white/70 p-4">
                  <div className="flex flex-col gap-2 lg:flex-row lg:items-start lg:justify-between">
                    <div>
                      <div className="text-sm font-semibold text-ink">{rule.id || "Untitled rule"}</div>
                      <div className="text-xs text-muted">{rule.reason || "No reason provided"}</div>
                    </div>
                    <Badge variant={decision.variant}>{decision.label}</Badge>
                  </div>
                  <div className="mt-2 flex flex-wrap gap-2 text-[11px] text-muted">
                    <span>Source {sourceLabel}</span>
                    {source?.version ? <span>Version {source.version}</span> : null}
                    {source?.installed_at ? <span>Installed {formatRelative(source.installed_at)}</span> : null}
                  </div>
                  {rule.match ? (
                    <pre className="mt-3 rounded-2xl border border-border bg-white/70 p-3 text-[11px] text-ink">
                      {JSON.stringify(rule.match, null, 2)}
                    </pre>
                  ) : null}
                  {rule.constraints ? (
                    <pre className="mt-3 rounded-2xl border border-border bg-white/70 p-3 text-[11px] text-ink">
                      {JSON.stringify(rule.constraints, null, 2)}
                    </pre>
                  ) : null}
                </div>
              );
            })}
          </div>
        )}
        {policyRuleErrors.length ? (
          <div className="mt-3 rounded-2xl border border-dashed border-border p-4 text-xs text-muted">
            {policyRuleErrors.map((err) => (
              <div key={err.fragment_id}>Fragment {err.fragment_id}: {err.error}</div>
            ))}
          </div>
        ) : null}
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Policy Simulator</CardTitle>
          <div className="text-xs text-muted">Simulate, explain, and evaluate decisions</div>
        </CardHeader>
        <form
          className="grid gap-3 lg:grid-cols-3"
          onSubmit={handleSubmit((values) => policyMutation.mutate(values))}
        >
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Topic</label>
            <Input {...register("topic")} />
            {errors.topic ? <div className="text-xs text-danger">{errors.topic.message}</div> : null}
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Tenant</label>
            <Input {...register("tenantId")} />
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Actor Type</label>
            <Select {...register("actorType")}>
              <option value="service">service</option>
              <option value="human">human</option>
            </Select>
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Workflow</label>
            <Input {...register("workflowId")} placeholder="workflow id" />
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Step</label>
            <Input {...register("stepId")} placeholder="step id" />
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Capability</label>
            <Input {...register("capability")} placeholder="capability" />
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Pack ID</label>
            <Input {...register("packId")} placeholder="pack id" />
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Risk Tags</label>
            <Input {...register("riskTags")} placeholder="comma-separated" />
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Requires</label>
            <Input {...register("requires")} placeholder="comma-separated" />
          </div>
          <div className="lg:col-span-3">
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Estimated Cost</label>
            <Input {...register("estimatedCost")} placeholder="optional" />
          </div>
          <div className="lg:col-span-3 flex flex-wrap gap-2">
            <Button variant={mode === "simulate" ? "primary" : "outline"} type="button" onClick={() => setMode("simulate")}>
              Simulate
            </Button>
            <Button variant={mode === "explain" ? "primary" : "outline"} type="button" onClick={() => setMode("explain")}>
              Explain
            </Button>
            <Button variant={mode === "evaluate" ? "primary" : "outline"} type="button" onClick={() => setMode("evaluate")}>
              Evaluate
            </Button>
            <Button variant="subtle" type="submit" disabled={policyMutation.isPending}>
              Run
            </Button>
          </div>
        </form>
        <div className="mt-4 rounded-2xl border border-border bg-white/70 p-4 text-xs text-muted">
          <div className="mb-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted">Response</div>
          <pre className="text-ink">{JSON.stringify(response || {}, null, 2)}</pre>
        </div>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Policy Diff</CardTitle>
          <div className="text-xs text-muted">Compare current bundles against a saved snapshot</div>
        </CardHeader>
        <div className="flex flex-col gap-3 lg:flex-row lg:items-center">
          <Input
            value={snapshotNote}
            onChange={(event) => setSnapshotNote(event.target.value)}
            placeholder="Snapshot note (optional)"
          />
          <Button
            variant="outline"
            type="button"
            onClick={() => captureSnapshotMutation.mutate(snapshotNote || undefined)}
            disabled={captureSnapshotMutation.isPending}
          >
            {captureSnapshotMutation.isPending ? "Capturing..." : "Capture snapshot"}
          </Button>
          <Button
            variant="subtle"
            type="button"
            onClick={() => {
              setSelectedSnapshotId("");
              setCompareText("");
            }}
          >
            Clear compare
          </Button>
        </div>
        {bundleSnapshots.length ? (
          <div className="mt-4 grid gap-3 lg:grid-cols-2">
            {bundleSnapshots.map((snapshot) => (
              <button
                key={snapshot.id}
                type="button"
                onClick={() => setSelectedSnapshotId(snapshot.id)}
                className={`rounded-2xl border p-4 text-left transition ${
                  snapshot.id === selectedSnapshotId
                    ? "border-accent bg-[color:rgba(15,127,122,0.12)]"
                    : "border-border bg-white/70 hover:border-accent"
                }`}
              >
                <div className="text-sm font-semibold text-ink">{snapshot.id}</div>
                <div className="text-xs text-muted">{formatRelative(snapshot.created_at)}</div>
                {snapshot.note ? <div className="text-xs text-muted">{snapshot.note}</div> : null}
              </button>
            ))}
          </div>
        ) : (
          <div className="mt-4 rounded-2xl border border-dashed border-border p-4 text-sm text-muted">
            No saved policy bundle snapshots yet.
          </div>
        )}
        <div className="grid gap-4 lg:grid-cols-2">
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Current bundles</label>
            <Textarea readOnly rows={12} value={currentBundlesText} />
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Compare to</label>
            <Textarea
              rows={12}
              value={compareText}
              onChange={(event) => setCompareText(event.target.value)}
              placeholder="Paste policy bundle JSON to compare"
            />
          </div>
        </div>
        {compareText.trim() ? (
          <div className="mt-4 rounded-2xl border border-border bg-white/70 p-4">
            <div className="grid gap-4 lg:grid-cols-2">
              <div className="space-y-1 text-[11px] font-mono">
                {diffLines.map((line, index) => (
                  <div
                    key={`left-${index}`}
                    className={`whitespace-pre rounded px-2 py-1 ${
                      line.match ? "text-muted" : "bg-[color:rgba(15,127,122,0.12)] text-ink"
                    }`}
                  >
                    {line.left || " "}
                  </div>
                ))}
              </div>
              <div className="space-y-1 text-[11px] font-mono">
                {diffLines.map((line, index) => (
                  <div
                    key={`right-${index}`}
                    className={`whitespace-pre rounded px-2 py-1 ${
                      line.match ? "text-muted" : "bg-[color:rgba(184,58,58,0.12)] text-ink"
                    }`}
                  >
                    {line.right || " "}
                  </div>
                ))}
              </div>
            </div>
          </div>
        ) : (
          <div className="mt-4 rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
            Paste a previous policy bundle JSON to view a line-by-line diff.
          </div>
        )}
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Policy Snapshots</CardTitle>
          <div className="text-xs text-muted">Policy version history</div>
        </CardHeader>
        {kernelSnapshots.length ? (
          <div className="space-y-3">
            {kernelSnapshots.map((snapshot, index) => (
              <div key={`snap-${index}`} className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="text-sm font-semibold text-ink">{snapshot}</div>
                <div className="text-xs text-muted">Snapshot {index + 1}</div>
              </div>
            ))}
          </div>
        ) : (
          <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">No snapshots recorded.</div>
        )}
      </Card>

      <Drawer open={Boolean(selectedApproval)} onClose={() => setSelectedApproval(null)}>
        {selectedApproval ? (
          <div className="space-y-5">
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Approval Detail</div>
            <div className="flex items-center justify-between">
              <div>
                <div className="text-sm font-semibold text-ink">Job {selectedApproval.job.id}</div>
                <div className="text-xs text-muted">Topic {selectedApproval.job.topic || "-"}</div>
              </div>
              <Badge variant={decisionBadgeMeta(selectedApproval.decision).variant}>
                {decisionBadgeMeta(selectedApproval.decision).label}
              </Badge>
            </div>
            <div className="flex flex-wrap gap-2">
              <ApprovalStatusBadge required={selectedApproval.approval_required} />
              <JobStatusBadge state={selectedApproval.job.state} />
            </div>
            <div className="rounded-2xl border border-border bg-white/70 p-4 text-xs text-muted">
              <div>Rule: {selectedApproval.policy_rule_id || "-"}</div>
              <div>Snapshot: {selectedApproval.policy_snapshot || "-"}</div>
              <div>Reason: {selectedApproval.policy_reason || "-"}</div>
              <div>Capability: {selectedApproval.job.capability || "-"}</div>
              <div>Pack: {selectedApproval.job.pack_id || "-"}</div>
            </div>
            {selectedApproval.constraints ? (
              <div>
                <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Constraints</div>
                <pre className="mt-2 rounded-2xl border border-border bg-white/70 p-3 text-[11px] text-ink">
                  {JSON.stringify(selectedApproval.constraints, null, 2)}
                </pre>
              </div>
            ) : null}
          </div>
        ) : null}
      </Drawer>
    </div>
  );
}
