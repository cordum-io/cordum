import { useEffect, useMemo, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { z } from "zod";
import { Link, useSearchParams } from "react-router-dom";
import { Info } from "lucide-react";
import { api } from "../lib/api";
import { formatRelative } from "../lib/format";
import { decisionTypeMeta } from "../lib/status";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Badge } from "../components/ui/Badge";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Textarea } from "../components/ui/Textarea";
import { Drawer } from "../components/ui/Drawer";
import { ApprovalStatusBadge, JobStatusBadge } from "../components/StatusBadge";
import { useConfigStore } from "../state/config";
import type { ApprovalItem, PolicyAuditEntry, PolicyBundleSummary, PolicyRule } from "../types/api";

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

type BundleDraft = {
  id: string;
  content: string;
  enabled: boolean;
  author: string;
  message: string;
};

const defaultBundleTemplate = "version: v1\nrules: []\n";

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

function buildPolicyRequest(values: FormValues) {
  return {
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
}

function normalizeBundleId(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) {
    return "";
  }
  return trimmed.startsWith("secops/") ? trimmed : `secops/${trimmed}`;
}

function normalizeRole(role: string): string {
  const trimmed = role.trim().toLowerCase();
  if (trimmed === "secops" || trimmed === "operator") {
    return "admin";
  }
  return trimmed;
}

function bundleSourceLabel(source?: string): string {
  const trimmed = source?.trim().toLowerCase();
  if (!trimmed) {
    return "core";
  }
  if (trimmed === "secops") {
    return "admin";
  }
  return trimmed;
}

export function PolicyPage() {
  const queryClient = useQueryClient();
  const principalRole = useConfigStore((state) => state.principalRole);
  const principalId = useConfigStore((state) => state.principalId);
  const canEditPolicy = normalizeRole(principalRole) === "admin";
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
  const policyAuditQuery = useQuery({
    queryKey: ["policy", "audit"],
    queryFn: () => api.listPolicyAudit(),
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
  const [bundleSimResponse, setBundleSimResponse] = useState<Record<string, unknown> | null>(null);
  const [bundleSaveInfo, setBundleSaveInfo] = useState<{ id: string; updated_at: string } | null>(null);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [bulkReason, setBulkReason] = useState("");
  const [bulkNote, setBulkNote] = useState("");
  const [selectedApproval, setSelectedApproval] = useState<ApprovalItem | null>(null);
  const [compareText, setCompareText] = useState("");
  const [selectedSnapshotId, setSelectedSnapshotId] = useState("");
  const [snapshotNote, setSnapshotNote] = useState("");
  const [showSafeOnly, setShowSafeOnly] = useState(false);
  const [activeTab, setActiveTab] = useState<"inbox" | "studio" | "explorer">("inbox");
  const [confirmBulkApprove, setConfirmBulkApprove] = useState<{ ids: string[]; type: "selected" | "safe" } | null>(null);
  const [bundleFilter, setBundleFilter] = useState<"all" | "secops" | "pack" | "core">("all");
  const [selectedBundleId, setSelectedBundleId] = useState("");
  const [bundleDraft, setBundleDraft] = useState<BundleDraft>({
    id: "",
    content: "",
    enabled: true,
    author: "",
    message: "",
  });
  const [newBundleName, setNewBundleName] = useState("");
  const [publishAuthor, setPublishAuthor] = useState("");
  const [publishMessage, setPublishMessage] = useState("");
  const [publishNote, setPublishNote] = useState("");
  const [publishSelection, setPublishSelection] = useState<Set<string>>(new Set());
  const [publishResult, setPublishResult] = useState<Record<string, unknown> | null>(null);
  const [rollbackSnapshotId, setRollbackSnapshotId] = useState("");
  const [rollbackNote, setRollbackNote] = useState("");
  const [rollbackResult, setRollbackResult] = useState<Record<string, unknown> | null>(null);

  const snapshotDetailQuery = useQuery({
    queryKey: ["policy", "bundle-snapshot", selectedSnapshotId],
    queryFn: () => api.getPolicyBundleSnapshot(selectedSnapshotId),
    enabled: Boolean(selectedSnapshotId),
  });

  const bundleItems = useMemo<PolicyBundleSummary[]>(() => {
    const items = (policyBundlesQuery.data?.items || []) as PolicyBundleSummary[];
    if (items.length) {
      return items;
    }
    const bundles = (policyBundlesQuery.data?.bundles || {}) as Record<string, unknown>;
    return Object.keys(bundles).sort().map((id) => ({
      id,
      enabled: true,
      source: id.startsWith("secops/") ? "secops" : id.includes("/") ? "pack" : "core",
    }));
  }, [policyBundlesQuery.data]);

  const filteredBundles = useMemo(() => {
    if (bundleFilter === "all") {
      return bundleItems;
    }
    return bundleItems.filter((item) => item.source === bundleFilter);
  }, [bundleFilter, bundleItems]);

  const selectedBundle = useMemo(
    () => bundleItems.find((item) => item.id === selectedBundleId),
    [bundleItems, selectedBundleId]
  );
  const isSecopsBundle = selectedBundleId.startsWith("secops/");
  const isEditableBundle = isSecopsBundle && canEditPolicy;
  const bundleAccessLabel = isSecopsBundle
    ? canEditPolicy
      ? "Editable admin bundle"
      : "Read-only (admin role required)"
    : "Read-only bundle";

  const policyBundleDetailQuery = useQuery({
    queryKey: ["policy", "bundle", selectedBundleId],
    queryFn: () => api.getPolicyBundle(selectedBundleId),
    enabled: Boolean(selectedBundleId && selectedBundle),
    refetchOnWindowFocus: false,
  });

  const bundleSaveMutation = useMutation({
    mutationFn: () =>
      api.putPolicyBundle(bundleDraft.id, {
        content: bundleDraft.content,
        enabled: bundleDraft.enabled,
        author: bundleDraft.author || undefined,
        message: bundleDraft.message || undefined,
      }),
    onSuccess: (data) => {
      const payload = data as { id: string; updated_at: string };
      setBundleSaveInfo(payload);
      queryClient.invalidateQueries({ queryKey: ["policy", "bundles"] });
      queryClient.invalidateQueries({ queryKey: ["policy", "rules"] });
      queryClient.invalidateQueries({ queryKey: ["policy", "bundle", bundleDraft.id] });
    },
  });

  const publishMutation = useMutation({
    mutationFn: (payload: { bundle_ids?: string[]; author?: string; message?: string; note?: string }) =>
      api.publishPolicyBundles(payload),
    onSuccess: (data) => {
      setPublishResult(data as Record<string, unknown>);
      queryClient.invalidateQueries({ queryKey: ["policy", "bundles"] });
      queryClient.invalidateQueries({ queryKey: ["policy", "rules"] });
      queryClient.invalidateQueries({ queryKey: ["policy", "audit"] });
      queryClient.invalidateQueries({ queryKey: ["policy", "bundle-snapshots"] });
    },
  });

  const rollbackMutation = useMutation({
    mutationFn: (payload: { snapshot_id: string; author?: string; message?: string; note?: string }) =>
      api.rollbackPolicyBundles(payload),
    onSuccess: (data) => {
      setRollbackResult(data as Record<string, unknown>);
      queryClient.invalidateQueries({ queryKey: ["policy", "bundles"] });
      queryClient.invalidateQueries({ queryKey: ["policy", "rules"] });
      queryClient.invalidateQueries({ queryKey: ["policy", "audit"] });
    },
  });

  const bundleSimMutation = useMutation({
    mutationFn: async (values: FormValues) => {
      const request = buildPolicyRequest(values);
      return api.simulatePolicyBundle(selectedBundleId, {
        request,
        content: bundleDraft.content,
      });
    },
    onSuccess: (data) => setBundleSimResponse(data as Record<string, unknown>),
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

  const bundleForm = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      topic: "job.default",
      tenantId: "default",
      actorType: "service",
    },
  });

  const policyMutation = useMutation({
    mutationFn: async (values: FormValues) => {
      const request = buildPolicyRequest(values);
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
  const auditEntries = useMemo<PolicyAuditEntry[]>(() => policyAuditQuery.data?.items ?? [], [policyAuditQuery.data]);

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

  useEffect(() => {
    if (!selectedBundleId && bundleItems.length) {
      setSelectedBundleId(bundleItems[0].id);
    }
  }, [bundleItems, selectedBundleId]);

  useEffect(() => {
    setBundleSimResponse(null);
  }, [selectedBundleId]);

  useEffect(() => {
    setBundleSaveInfo(null);
  }, [selectedBundleId]);

  useEffect(() => {
    if (!selectedBundleId || !selectedBundle) {
      return;
    }
    if (!policyBundleDetailQuery.data) {
      return;
    }
    const detail = policyBundleDetailQuery.data;
    setBundleDraft({
      id: detail.id,
      content: detail.content || "",
      enabled: detail.enabled,
      author: detail.author || "",
      message: detail.message || "",
    });
  }, [policyBundleDetailQuery.data, selectedBundle, selectedBundleId]);

  useEffect(() => {
    if (!selectedBundleId || selectedBundle) {
      return;
    }
    if (bundleDraft.id === selectedBundleId) {
      return;
    }
    setBundleDraft({
      id: selectedBundleId,
      content: defaultBundleTemplate,
      enabled: true,
      author: "",
      message: "",
    });
  }, [bundleDraft.id, selectedBundle, selectedBundleId]);

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

  const secopsBundles = useMemo(
    () => bundleItems.filter((item) => item.id.startsWith("secops/")),
    [bundleItems]
  );
  const allSecopsSelected = secopsBundles.length > 0 && secopsBundles.every((item) => publishSelection.has(item.id));
  const publishLabel = publishSelection.size ? "Publish selected" : "Publish all admin bundles";
  const canPublish = canEditPolicy && secopsBundles.length > 0;

  const togglePublishSelection = (bundleId: string) => {
    setPublishSelection((prev) => {
      const next = new Set(prev);
      if (next.has(bundleId)) {
        next.delete(bundleId);
      } else {
        next.add(bundleId);
      }
      return next;
    });
  };

  const setAllPublishSelection = (checked: boolean) => {
    if (!checked) {
      setPublishSelection(new Set());
      return;
    }
    setPublishSelection(new Set(secopsBundles.map((item) => item.id)));
  };

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Policy Center</CardTitle>
          <div className="text-xs text-muted">Approvals, studio tooling, and decision exploration.</div>
        </CardHeader>
        <div className="flex flex-wrap gap-2">
          <Button variant={activeTab === "inbox" ? "primary" : "outline"} size="sm" onClick={() => setActiveTab("inbox")}>Inbox</Button>
          <Button variant={activeTab === "studio" ? "primary" : "outline"} size="sm" onClick={() => setActiveTab("studio")}>Policy Studio</Button>
          <Button variant={activeTab === "explorer" ? "primary" : "outline"} size="sm" onClick={() => setActiveTab("explorer")}>Decision Explorer</Button>
        </div>
      </Card>

      {activeTab === "inbox" ? (
        <>
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
                    <label className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted" title="Safe = no risk_tags, no requires constraints, no policy constraints">
                      <input
                        type="checkbox"
                        checked={showSafeOnly}
                        onChange={(event) => setShowSafeOnly(event.target.checked)}
                      />
                      Only safe
                      <span className="relative group">
                        <Info className="h-3 w-3 text-muted cursor-help" />
                        <span className="absolute left-1/2 -translate-x-1/2 bottom-full mb-2 hidden group-hover:block w-48 p-2 text-[10px] normal-case tracking-normal font-normal bg-ink text-white rounded-lg shadow-lg z-10">
                          Safe items have no risk_tags, no requires constraints, and no policy constraints applied.
                        </span>
                      </span>
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
                        onClick={() => setConfirmBulkApprove({ ids: Array.from(selectedIds), type: "selected" })}
                        disabled={selectedCount === 0 || bulkApproveMutation.isPending || bulkRejectMutation.isPending}
                      >
                        Approve {selectedCount || ""}
                      </Button>
                      <Button
                        variant="subtle"
                        size="sm"
                        type="button"
                        onClick={() => setConfirmBulkApprove({ ids: safeApprovals.map((item) => item.job.id), type: "safe" })}
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
                    const riskTags = item.job.risk_tags || [];
                    const hasRisks = riskTags.length > 0;
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
                          <div className="flex flex-col gap-1">
                            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">
                              {item.job.actor_id ? (
                                <span>
                                  <span className="text-ink/70">Actor:</span>{" "}
                                  {item.job.actor_type ? `${item.job.actor_type}/` : ""}
                                  {item.job.actor_id}
                                </span>
                              ) : null}
                              {item.job.capability ? (
                                <span>
                                  <span className="text-ink/70">Cap:</span> {item.job.capability}
                                </span>
                              ) : null}
                              {item.job.pack_id ? (
                                <span>
                                  <span className="text-ink/70">Pack:</span> {item.job.pack_id}
                                </span>
                              ) : null}
                              <span>
                                <span className="text-ink/70">Tenant:</span> {item.job.tenant || "default"}
                              </span>
                            </div>
                            {hasRisks ? (
                              <div className="flex flex-wrap items-center gap-1">
                                <span className="text-xs text-ink/70">Risk:</span>
                                {riskTags.map((tag) => (
                                  <span
                                    key={tag}
                                    className="inline-flex items-center rounded bg-danger/10 px-1.5 py-0.5 text-[10px] font-medium text-danger"
                                  >
                                    {tag}
                                  </span>
                                ))}
                              </div>
                            ) : null}
                            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">
                              {item.policy_rule_id ? (
                                <span>
                                  <span className="text-ink/70">Rule:</span> {item.policy_rule_id}
                                </span>
                              ) : null}
                              {item.policy_snapshot ? (
                                <span title={item.policy_snapshot}>
                                  <span className="text-ink/70">Snapshot:</span> {item.policy_snapshot.slice(0, 8)}
                                </span>
                              ) : null}
                              {item.policy_reason ? (
                                <span className="text-warning">{item.policy_reason}</span>
                              ) : null}
                            </div>
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
                          <Link to={`/jobs/${item.job.id}`}>
                            <Button variant="outline" size="sm" type="button">
                              Job
                            </Button>
                          </Link>
                          <Link to={`/jobs/${item.job.id}?tab=safety`}>
                            <Button variant="outline" size="sm" type="button">
                              Decisions
                            </Button>
                          </Link>
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
        </>
      ) : null}

      {activeTab === "studio" ? (
        <>
          <Card>
          <CardHeader>
            <CardTitle>Policy Bundles</CardTitle>
            <div className="text-xs text-muted">Edit admin bundles (secops/*), inspect pack fragments, and preview content.</div>
          </CardHeader>
          <div className="rounded-2xl border border-border bg-white/70 p-3 text-xs text-muted">
            <div>
              Role {principalRole || "unset"}
              {principalId ? ` · Principal ${principalId}` : ""}
              {canEditPolicy ? " · Editing enabled" : " · Read-only"}
            </div>
            {!canEditPolicy ? <div className="mt-1">Set `principalRole` to `admin` in config.json to edit/publish.</div> : null}
          </div>
          <div className="grid gap-4 lg:grid-cols-[minmax(0,240px)_minmax(0,1fr)]">
              <div className="space-y-4">
                <div className="rounded-2xl border border-border bg-white/70 p-3">
                  <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">New bundle</div>
                  <div className="mt-2 flex gap-2">
                    <Input
                      value={newBundleName}
                      onChange={(event) => setNewBundleName(event.target.value)}
                      placeholder="secops/workflows"
                      disabled={!canEditPolicy}
                    />
                    <Button
                      variant="outline"
                      size="sm"
                      type="button"
                      onClick={() => {
                        const nextId = normalizeBundleId(newBundleName);
                        if (!nextId) {
                          return;
                        }
                        setSelectedBundleId(nextId);
                        setBundleDraft({
                          id: nextId,
                          content: defaultBundleTemplate,
                          enabled: true,
                          author: "",
                          message: "",
                        });
                        setNewBundleName("");
                      }}
                      disabled={!canEditPolicy || !newBundleName.trim()}
                    >
                      Create
                    </Button>
                  </div>
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Filter</label>
                  <Select value={bundleFilter} onChange={(event) => setBundleFilter(event.target.value as typeof bundleFilter)}>
                    <option value="all">All bundles</option>
                    <option value="secops">Admin</option>
                    <option value="pack">Packs</option>
                    <option value="core">Core</option>
                  </Select>
                </div>
                {filteredBundles.length ? (
                  <div className="space-y-2">
                    {filteredBundles.map((bundle) => {
                      const selected = bundle.id === selectedBundleId;
                      return (
                        <button
                          key={bundle.id}
                          type="button"
                          onClick={() => setSelectedBundleId(bundle.id)}
                          className={`list-row w-full text-left ${
                            selected ? "border-accent bg-[color:rgba(15,127,122,0.16)]" : ""
                          }`}
                        >
                          <div className="flex items-center justify-between gap-2">
                            <div>
                              <div className="text-sm font-semibold text-ink">{bundle.id}</div>
                              <div className="text-xs text-muted">
                                {bundleSourceLabel(bundle.source)}
                                {bundle.updated_at ? ` · Updated ${formatRelative(bundle.updated_at)}` : ""}
                                {bundle.installed_at ? ` · Installed ${formatRelative(bundle.installed_at)}` : ""}
                              </div>
                            </div>
                            <Badge variant={bundle.enabled ? "success" : "warning"}>{bundle.enabled ? "Enabled" : "Disabled"}</Badge>
                          </div>
                        </button>
                      );
                    })}
                  </div>
                ) : (
                  <div className="rounded-2xl border border-dashed border-border p-4 text-sm text-muted">
                    No bundles in this filter.
                  </div>
                )}
              </div>
              <div className="space-y-4">
                {selectedBundleId ? (
                  <>
                    <div className="rounded-2xl border border-border bg-white/70 p-4">
                      <div className="flex flex-col gap-2 lg:flex-row lg:items-start lg:justify-between">
                        <div>
                          <div className="text-sm font-semibold text-ink">{selectedBundleId}</div>
                        <div className="text-xs text-muted">{bundleAccessLabel}</div>
                        </div>
                        <Badge variant={selectedBundle?.source === "secops" ? "info" : "default"}>
                          {bundleSourceLabel(selectedBundle?.source)}
                        </Badge>
                      </div>
                      <div className="mt-3 grid gap-3 lg:grid-cols-2">
                        <div>
                          <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Enabled</label>
                          <Select
                            value={bundleDraft.enabled ? "enabled" : "disabled"}
                            onChange={(event) =>
                              setBundleDraft((prev) => ({
                                ...prev,
                                enabled: event.target.value === "enabled",
                              }))
                            }
                            disabled={!isEditableBundle}
                          >
                            <option value="enabled">Enabled</option>
                            <option value="disabled">Disabled</option>
                          </Select>
                        </div>
                        <div>
                          <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Author</label>
                          <Input
                            value={bundleDraft.author}
                            onChange={(event) => setBundleDraft((prev) => ({ ...prev, author: event.target.value }))}
                            placeholder="admin@cordum.io"
                            disabled={!isEditableBundle}
                          />
                        </div>
                        <div className="lg:col-span-2">
                          <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Message</label>
                          <Input
                            value={bundleDraft.message}
                            onChange={(event) => setBundleDraft((prev) => ({ ...prev, message: event.target.value }))}
                            placeholder="Change intent"
                            disabled={!isEditableBundle}
                          />
                        </div>
                      </div>
                    </div>
                    <div>
                      <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Bundle content</label>
                      <Textarea
                        rows={14}
                        value={bundleDraft.content}
                        onChange={(event) => setBundleDraft((prev) => ({ ...prev, content: event.target.value }))}
                        placeholder="YAML policy bundle"
                        readOnly={!isEditableBundle}
                      />
                    </div>
                    <div className="flex flex-wrap gap-2">
                      <Button
                        variant="primary"
                        size="sm"
                        type="button"
                        onClick={() => bundleSaveMutation.mutate()}
                        disabled={!isEditableBundle || !bundleDraft.content.trim() || bundleSaveMutation.isPending}
                      >
                        {bundleSaveMutation.isPending ? "Saving..." : "Save bundle"}
                      </Button>
                      {policyBundleDetailQuery.isFetching ? (
                        <span className="text-xs text-muted">Refreshing bundle...</span>
                      ) : null}
                      {bundleSaveInfo?.id === bundleDraft.id ? (
                        <span className="text-xs text-muted">Saved {formatRelative(bundleSaveInfo.updated_at)}</span>
                      ) : null}
                    </div>
                  </>
                ) : (
                  <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
                    Select a bundle to inspect or edit.
                  </div>
                )}
              </div>
            </div>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Publish & Rollback</CardTitle>
              <div className="text-xs text-muted">Publish admin bundles (secops/*) and roll back with snapshots.</div>
            </CardHeader>
            <div className="grid gap-4 lg:grid-cols-2">
              <div className="space-y-3">
                <div className="flex items-center justify-between">
                  <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Admin bundles (secops/*)</div>
                  <label className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted">
                    <input
                      type="checkbox"
                      checked={allSecopsSelected}
                      onChange={(event) => setAllPublishSelection(event.target.checked)}
                      disabled={!canEditPolicy}
                    />
                    Select all
                  </label>
                </div>
                {secopsBundles.length ? (
                  <div className="space-y-2">
                    {secopsBundles.map((bundle) => (
                      <div key={bundle.id} className="list-row flex items-center justify-between">
                        <label className="flex items-center gap-2 text-sm text-ink">
                          <input
                            type="checkbox"
                            checked={publishSelection.has(bundle.id)}
                            onChange={() => togglePublishSelection(bundle.id)}
                            disabled={!canEditPolicy}
                          />
                          {bundle.id}
                        </label>
                        <Badge variant={bundle.enabled ? "success" : "warning"}>{bundle.enabled ? "Enabled" : "Disabled"}</Badge>
                      </div>
                    ))}
                  </div>
                ) : (
                  <div className="rounded-2xl border border-dashed border-border p-4 text-sm text-muted">
                    No admin bundles available yet.
                  </div>
                )}
                <div className="space-y-2">
                  <Input
                    value={publishAuthor}
                    onChange={(event) => setPublishAuthor(event.target.value)}
                    placeholder="Author (optional)"
                  />
                  <Input
                    value={publishMessage}
                    onChange={(event) => setPublishMessage(event.target.value)}
                    placeholder="Publish message (optional)"
                  />
                  <Input
                    value={publishNote}
                    onChange={(event) => setPublishNote(event.target.value)}
                    placeholder="Snapshot note (optional)"
                  />
                  {!canEditPolicy ? (
                    <div className="text-xs text-muted">Publishing requires `principalRole=admin`.</div>
                  ) : publishSelection.size === 0 ? (
                    <div className="text-xs text-muted">No bundles selected; publish will include all admin bundles.</div>
                  ) : null}
                  <div className="flex flex-wrap gap-2">
                    <Button
                      variant="primary"
                      size="sm"
                      type="button"
                      onClick={() =>
                        publishMutation.mutate({
                          bundle_ids: publishSelection.size ? Array.from(publishSelection) : undefined,
                          author: publishAuthor || undefined,
                          message: publishMessage || undefined,
                          note: publishNote || undefined,
                        })
                      }
                      disabled={!canPublish || publishMutation.isPending}
                    >
                      {publishMutation.isPending ? "Publishing..." : publishLabel}
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      type="button"
                      onClick={() => {
                        setPublishSelection(new Set());
                        setPublishAuthor("");
                        setPublishMessage("");
                        setPublishNote("");
                      }}
                    >
                      Clear
                    </Button>
                  </div>
                </div>
                {publishResult ? (
                  <pre className="rounded-2xl border border-border bg-white/70 p-3 text-[11px] text-ink">
                    {JSON.stringify(publishResult, null, 2)}
                  </pre>
                ) : null}
              </div>
              <div className="space-y-3">
                <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Rollback</div>
                <Select value={rollbackSnapshotId} onChange={(event) => setRollbackSnapshotId(event.target.value)}>
                  <option value="">Select snapshot</option>
                  {bundleSnapshots.map((snapshot) => (
                    <option key={snapshot.id} value={snapshot.id}>
                      {snapshot.id}
                    </option>
                  ))}
                </Select>
                <Input
                  value={rollbackNote}
                  onChange={(event) => setRollbackNote(event.target.value)}
                  placeholder="Rollback note (optional)"
                />
                <Button
                  variant="danger"
                  size="sm"
                  type="button"
                  onClick={() =>
                    rollbackMutation.mutate({
                      snapshot_id: rollbackSnapshotId,
                      note: rollbackNote || undefined,
                    })
                  }
                  disabled={!canEditPolicy || !rollbackSnapshotId || rollbackMutation.isPending}
                >
                  {rollbackMutation.isPending ? "Rolling back..." : "Rollback"}
                </Button>
                {rollbackResult ? (
                  <pre className="rounded-2xl border border-border bg-white/70 p-3 text-[11px] text-ink">
                    {JSON.stringify(rollbackResult, null, 2)}
                  </pre>
                ) : null}
              </div>
            </div>
          </Card>

          <Card>
          <CardHeader>
            <CardTitle>Bundle Simulation</CardTitle>
            <div className="text-xs text-muted">Simulate decisions against the current bundle draft.</div>
          </CardHeader>
          {selectedBundleId ? (
            <>
              <div className="rounded-2xl border border-border bg-white/70 p-3 text-xs text-muted">
                Simulation uses the draft content in the editor, even if it is not saved yet.
              </div>
              <form
                className="grid gap-3 lg:grid-cols-3"
                onSubmit={bundleForm.handleSubmit((values) => bundleSimMutation.mutate(values))}
              >
                  <div>
                    <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Topic</label>
                    <Input {...bundleForm.register("topic")} />
                    {bundleForm.formState.errors.topic ? (
                      <div className="text-xs text-danger">{bundleForm.formState.errors.topic.message}</div>
                    ) : null}
                  </div>
                  <div>
                    <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Tenant</label>
                    <Input {...bundleForm.register("tenantId")} />
                  </div>
                  <div>
                    <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Actor Type</label>
                    <Select {...bundleForm.register("actorType")}>
                      <option value="service">service</option>
                      <option value="human">human</option>
                    </Select>
                  </div>
                  <div>
                    <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Workflow</label>
                    <Input {...bundleForm.register("workflowId")} placeholder="workflow id" />
                  </div>
                  <div>
                    <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Step</label>
                    <Input {...bundleForm.register("stepId")} placeholder="step id" />
                  </div>
                  <div>
                    <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Capability</label>
                    <Input {...bundleForm.register("capability")} placeholder="capability" />
                  </div>
                  <div>
                    <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Pack ID</label>
                    <Input {...bundleForm.register("packId")} placeholder="pack id" />
                  </div>
                  <div>
                    <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Risk Tags</label>
                    <Input {...bundleForm.register("riskTags")} placeholder="comma-separated" />
                  </div>
                  <div>
                    <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Requires</label>
                    <Input {...bundleForm.register("requires")} placeholder="comma-separated" />
                  </div>
                  <div className="lg:col-span-3">
                    <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Estimated Cost</label>
                    <Input {...bundleForm.register("estimatedCost")} placeholder="optional" />
                  </div>
                  <div className="lg:col-span-3 flex flex-wrap gap-2">
                    <Button variant="subtle" type="submit" disabled={bundleSimMutation.isPending}>
                      {bundleSimMutation.isPending ? "Running..." : "Simulate"}
                    </Button>
                  </div>
                </form>
                <div className="mt-4 rounded-2xl border border-border bg-white/70 p-4 text-xs text-muted">
                  <div className="mb-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted">Response</div>
                  <pre className="text-ink">{JSON.stringify(bundleSimResponse || {}, null, 2)}</pre>
                </div>
              </>
            ) : (
              <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
                Select a bundle to simulate against its rules.
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
              <div className="text-xs text-muted">Safety kernel snapshot history</div>
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

          <Card>
            <CardHeader>
              <CardTitle>Policy Audit</CardTitle>
              <div className="text-xs text-muted">Publish and rollback history</div>
            </CardHeader>
            {policyAuditQuery.isLoading ? (
              <div className="text-sm text-muted">Loading audit entries...</div>
            ) : auditEntries.length ? (
              <div className="space-y-3">
                {auditEntries.map((entry) => (
                  <div key={entry.id} className="list-row">
                    <div className="flex flex-col gap-2 lg:flex-row lg:items-center lg:justify-between">
                      <div>
                        <div className="text-sm font-semibold text-ink">{entry.action.toUpperCase()}</div>
                        <div className="text-xs text-muted">
                          {entry.actor_id ? `Actor ${entry.actor_id}` : "Actor unknown"}
                          {entry.role ? ` · Role ${entry.role}` : ""}
                        </div>
                      </div>
                      <div className="text-xs text-muted">{formatRelative(entry.created_at)}</div>
                    </div>
                    {entry.bundle_ids?.length ? (
                      <div className="mt-2 text-xs text-muted">Bundles: {entry.bundle_ids.join(", ")}</div>
                    ) : null}
                    {(entry.snapshot_before || entry.snapshot_after) ? (
                      <div className="mt-2 text-xs text-muted">
                        {entry.snapshot_before ? `Before ${entry.snapshot_before}` : ""}
                        {entry.snapshot_after ? ` · After ${entry.snapshot_after}` : ""}
                      </div>
                    ) : null}
                    {entry.message ? <div className="mt-2 text-xs text-muted">{entry.message}</div> : null}
                  </div>
                ))}
              </div>
            ) : (
              <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
                No audit entries recorded yet.
              </div>
            )}
          </Card>
        </>
      ) : null}

      {activeTab === "explorer" ? (
        <>
          <Card>
            <CardHeader>
              <CardTitle>Decision Explorer</CardTitle>
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
        </>
      ) : null}

      {confirmBulkApprove ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
          <div className="w-full max-w-lg rounded-2xl bg-white p-6 shadow-xl dark:bg-[color:var(--surface-muted)]">
            <div className="text-lg font-semibold text-ink">Confirm Bulk Approval</div>
            <div className="mt-2 text-sm text-muted">
              You are about to approve {confirmBulkApprove.ids.length} job{confirmBulkApprove.ids.length === 1 ? "" : "s"}.
            </div>
            <div className="mt-4 max-h-48 overflow-y-auto rounded-xl border border-border bg-white/70 p-3">
              <div className="space-y-2 text-xs">
                {confirmBulkApprove.ids.slice(0, 10).map((id) => {
                  const item = approvals.find((a) => a.job.id === id);
                  if (!item) return null;
                  const riskTags = item.job.risk_tags || [];
                  return (
                    <div key={id} className="flex items-center justify-between gap-2 rounded-lg border border-border/50 bg-white/50 p-2">
                      <div>
                        <div className="font-medium text-ink">{item.job.topic || `Job ${id.slice(0, 8)}`}</div>
                        <div className="text-muted">
                          {item.job.capability ? `Cap: ${item.job.capability}` : ""}
                          {item.job.pack_id ? ` · Pack: ${item.job.pack_id}` : ""}
                        </div>
                      </div>
                      {riskTags.length > 0 ? (
                        <div className="flex flex-wrap gap-1">
                          {riskTags.map((tag) => (
                            <span key={tag} className="rounded bg-danger/10 px-1.5 py-0.5 text-[10px] font-medium text-danger">
                              {tag}
                            </span>
                          ))}
                        </div>
                      ) : (
                        <span className="rounded bg-success/10 px-1.5 py-0.5 text-[10px] font-medium text-success">safe</span>
                      )}
                    </div>
                  );
                })}
                {confirmBulkApprove.ids.length > 10 ? (
                  <div className="text-center text-muted">...and {confirmBulkApprove.ids.length - 10} more</div>
                ) : null}
              </div>
            </div>
            <div className="mt-4 flex justify-end gap-2">
              <Button variant="outline" size="sm" type="button" onClick={() => setConfirmBulkApprove(null)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                size="sm"
                type="button"
                onClick={() => {
                  bulkApproveMutation.mutate({
                    ids: confirmBulkApprove.ids,
                    reason: bulkReason || undefined,
                    note: bulkNote || undefined,
                  });
                  setConfirmBulkApprove(null);
                }}
                disabled={bulkApproveMutation.isPending}
              >
                Confirm Approval ({confirmBulkApprove.ids.length})
              </Button>
            </div>
          </div>
        </div>
      ) : null}

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
