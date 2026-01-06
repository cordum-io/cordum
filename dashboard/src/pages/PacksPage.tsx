import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";
import { api } from "../lib/api";
import { formatPercent, formatRelative } from "../lib/format";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Badge } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { Drawer } from "../components/ui/Drawer";
import { Input } from "../components/ui/Input";
import type { Heartbeat, PackRecord, PackVerifyResponse } from "../types/api";

function statusVariant(status?: string): "success" | "warning" | "danger" | "default" {
  const normalized = (status || "").toUpperCase();
  if (normalized === "ACTIVE") {
    return "success";
  }
  if (normalized === "INACTIVE") {
    return "warning";
  }
  if (normalized === "DISABLED") {
    return "danger";
  }
  return "default";
}

function subjectMatches(pattern: string, subject: string): boolean {
  if (!pattern || !subject) {
    return false;
  }
  const pTokens = pattern.split(".");
  const sTokens = subject.split(".");
  for (let i = 0; i < pTokens.length; i += 1) {
    const token = pTokens[i];
    if (token === ">") {
      return true;
    }
    if (sTokens.length <= i) {
      return false;
    }
    if (token === "*") {
      continue;
    }
    if (token !== sTokens[i]) {
      return false;
    }
  }
  return sTokens.length === pTokens.length;
}

export function PacksPage() {
  const queryClient = useQueryClient();
  const [searchParams] = useSearchParams();
  const packsQuery = useQuery({
    queryKey: ["packs"],
    queryFn: () => api.listPacks(),
  });
  const workersQuery = useQuery({
    queryKey: ["workers"],
    queryFn: () => api.listWorkers(),
  });
  const packs = useMemo(() => packsQuery.data?.items ?? [], [packsQuery.data]);
  const workers = useMemo(() => (workersQuery.data || []) as Heartbeat[], [workersQuery.data]);
  const packWorkers = useMemo(() => {
    const map = new Map<string, Heartbeat[]>();
    packs.forEach((pack) => {
      const topics =
        pack.manifest?.topics
          ?.map((topic) => topic.name)
          .filter((topic): topic is string => Boolean(topic)) || [];
      if (topics.length === 0) {
        map.set(pack.id, []);
        return;
      }
      const matches = workers.filter((worker) => {
        if (!worker.topic) {
          return false;
        }
        return topics.some((pattern) => subjectMatches(pattern, worker.topic as string));
      });
      map.set(pack.id, matches);
    });
    return map;
  }, [packs, workers]);

  const [bundleFile, setBundleFile] = useState<File | null>(null);
  const [forceInstall, setForceInstall] = useState(false);
  const [upgradeInstall, setUpgradeInstall] = useState(false);
  const [inactiveInstall, setInactiveInstall] = useState(false);
  const [purgeOnUninstall, setPurgeOnUninstall] = useState(false);
  const [selectedPack, setSelectedPack] = useState<PackRecord | null>(null);
  const [verifyResults, setVerifyResults] = useState<Record<string, PackVerifyResponse>>({});
  const selectedPackWorkers = useMemo(() => {
    if (!selectedPack) {
      return [];
    }
    return packWorkers.get(selectedPack.id) || [];
  }, [packWorkers, selectedPack]);
  const selectedPackImpact = useMemo(() => {
    if (!selectedPack) {
      return null;
    }
    const topics = selectedPack.manifest?.topics || [];
    const capabilities = new Set<string>();
    const requires = new Set<string>();
    const riskTags = new Set<string>();
    topics.forEach((topic) => {
      if (topic.capability) {
        capabilities.add(topic.capability);
      }
      (topic.requires || []).forEach((req) => requires.add(req));
      (topic.riskTags || []).forEach((tag) => riskTags.add(tag));
    });
    const workflowsCount = selectedPack.resources?.workflows ? Object.keys(selectedPack.resources.workflows).length : 0;
    const schemasCount = selectedPack.resources?.schemas ? Object.keys(selectedPack.resources.schemas).length : 0;
    const policyFragments = selectedPack.overlays?.policy || [];
    const configOverlays = selectedPack.overlays?.config || [];
    return {
      topicsCount: topics.length,
      workflowsCount,
      schemasCount,
      policyFragments,
      configOverlays,
      capabilities: Array.from(capabilities),
      requires: Array.from(requires),
      riskTags: Array.from(riskTags),
    };
  }, [selectedPack]);

  useEffect(() => {
    const packId = searchParams.get("pack_id") || searchParams.get("id") || "";
    if (!packId || packs.length === 0) {
      return;
    }
    const match = packs.find((pack) => pack.id === packId);
    if (match) {
      setSelectedPack(match);
    }
  }, [packs, searchParams]);

  const installMutation = useMutation({
    mutationFn: async () => {
      if (!bundleFile) {
        throw new Error("bundle required");
      }
      return api.installPack(bundleFile, {
        force: forceInstall,
        upgrade: upgradeInstall,
        inactive: inactiveInstall,
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["packs"] });
      setBundleFile(null);
    },
  });

  const uninstallMutation = useMutation({
    mutationFn: (packId: string) => api.uninstallPack(packId, purgeOnUninstall),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["packs"] }),
  });

  const verifyMutation = useMutation({
    mutationFn: (packId: string) => api.verifyPack(packId),
    onSuccess: (data) => {
      setVerifyResults((prev) => ({ ...prev, [data.pack_id]: data }));
    },
  });

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Install Pack</CardTitle>
          <div className="text-xs text-muted">Upload a .tgz bundle and configure install options</div>
        </CardHeader>
        <div className="space-y-3">
          <Input
            type="file"
            accept=".tgz,.tar.gz"
            onChange={(event) => setBundleFile(event.target.files?.[0] || null)}
          />
          <div className="flex flex-wrap gap-4 text-xs text-muted">
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={forceInstall}
                onChange={(event) => setForceInstall(event.target.checked)}
              />
              Force install (ignore minCoreVersion)
            </label>
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={upgradeInstall}
                onChange={(event) => setUpgradeInstall(event.target.checked)}
              />
              Upgrade existing pack
            </label>
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={inactiveInstall}
                onChange={(event) => setInactiveInstall(event.target.checked)}
              />
              Install inactive
            </label>
          </div>
          <Button
            variant="primary"
            type="button"
            onClick={() => installMutation.mutate()}
            disabled={!bundleFile || installMutation.isPending}
          >
            {installMutation.isPending ? "Installing..." : "Install pack"}
          </Button>
        </div>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Packs</CardTitle>
          <div className="text-xs text-muted">Installed pack registry</div>
        </CardHeader>
        <div className="mb-4 flex items-center justify-between text-xs text-muted">
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={purgeOnUninstall}
              onChange={(event) => setPurgeOnUninstall(event.target.checked)}
            />
            Purge resources on uninstall
          </label>
          <div>Loaded {packs.length} packs</div>
        </div>
        {packsQuery.isLoading ? (
          <div className="text-sm text-muted">Loading packs...</div>
        ) : packs.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
            No packs installed yet. Upload a bundle to get started.
          </div>
        ) : (
          <div className="grid gap-4 lg:grid-cols-2">
            {packs.map((pack) => {
              const verify = verifyResults[pack.id];
              const verifyOk = verify?.results.filter((result) => result.ok).length ?? 0;
              const verifyTotal = verify?.results.length ?? 0;
              const topics = pack.manifest?.topics || [];
              const packWorkerList = packWorkers.get(pack.id) || [];
              const cpuValues = packWorkerList.map((worker) => worker.cpu_load).filter((v): v is number => typeof v === "number");
              const memValues = packWorkerList.map((worker) => worker.memory_load).filter((v): v is number => typeof v === "number");
              const avgCpu = cpuValues.length ? cpuValues.reduce((sum, v) => sum + v, 0) / cpuValues.length : undefined;
              const avgMem = memValues.length ? memValues.reduce((sum, v) => sum + v, 0) / memValues.length : undefined;
              const resourceCount =
                (pack.resources?.schemas ? Object.keys(pack.resources.schemas).length : 0) +
                (pack.resources?.workflows ? Object.keys(pack.resources.workflows).length : 0);
              return (
                <div key={pack.id} className="rounded-2xl border border-border bg-white/70 p-5">
                  <div className="flex items-center justify-between">
                    <div>
                      <div className="text-sm font-semibold text-ink">{pack.manifest?.metadata?.title || pack.id}</div>
                      <div className="text-xs text-muted">
                        {pack.manifest?.metadata?.description || "No description"}
                      </div>
                    </div>
                    <Badge variant={statusVariant(pack.status)}>{pack.status}</Badge>
                  </div>
                  <div className="mt-3 text-xs text-muted">Version {pack.version || "-"}</div>
                  <div className="text-xs text-muted">Installed {formatRelative(pack.installed_at)}</div>
                  <div className="text-xs text-muted">
                    Workers {packWorkerList.length} · CPU {formatPercent(avgCpu)} · Mem {formatPercent(avgMem)}
                  </div>
                  <div className="mt-3 flex flex-wrap gap-2 text-[11px] text-muted">
                    <span>{resourceCount} resources</span>
                    <span>{pack.overlays?.config?.length || 0} config overlays</span>
                    <span>{pack.overlays?.policy?.length || 0} policy overlays</span>
                  </div>
                  {topics.length ? (
                    <div className="mt-3 flex flex-wrap gap-2">
                      {topics.slice(0, 4).map((topic, index) => (
                        <Badge key={`${pack.id}-topic-${index}`}>{topic.name || "topic"}</Badge>
                      ))}
                    </div>
                  ) : null}
                  {verify ? (
                    <div className="mt-3 text-xs text-muted">
                      Verify: {verifyOk}/{verifyTotal} simulations passed
                    </div>
                  ) : null}
                  <div className="mt-4 flex flex-wrap gap-2">
                    <Button variant="outline" size="sm" type="button" onClick={() => setSelectedPack(pack)}>
                      Details
                    </Button>
                    <Button
                      variant="subtle"
                      size="sm"
                      type="button"
                      onClick={() => verifyMutation.mutate(pack.id)}
                      disabled={verifyMutation.isPending}
                    >
                      Verify
                    </Button>
                    <Button
                      variant="danger"
                      size="sm"
                      type="button"
                      onClick={() => uninstallMutation.mutate(pack.id)}
                      disabled={uninstallMutation.isPending}
                    >
                      Uninstall
                    </Button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </Card>

      <Drawer open={Boolean(selectedPack)} onClose={() => setSelectedPack(null)}>
        {selectedPack ? (
          <div className="space-y-5">
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Pack Details</div>
            <div>
              <div className="text-sm font-semibold text-ink">{selectedPack.manifest?.metadata?.title || selectedPack.id}</div>
              <div className="text-xs text-muted">Version {selectedPack.version}</div>
            </div>
            <div className="rounded-2xl border border-border bg-white/70 p-4 text-xs text-muted">
              <div>Pack ID: {selectedPack.id}</div>
              <div>Status: {selectedPack.status}</div>
              <div>Installed: {formatRelative(selectedPack.installed_at)}</div>
              <div>Installed by: {selectedPack.installed_by || "-"}</div>
            </div>
            {selectedPackImpact ? (
              <div>
                <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Impact</div>
                <div className="mt-2 rounded-2xl border border-border bg-white/70 p-4 text-xs text-muted">
                  <div>Topics: {selectedPackImpact.topicsCount}</div>
                  <div>Workflows: {selectedPackImpact.workflowsCount}</div>
                  <div>Schemas: {selectedPackImpact.schemasCount}</div>
                  <div>Policy fragments: {selectedPackImpact.policyFragments.length}</div>
                  <div>Config overlays: {selectedPackImpact.configOverlays.length}</div>
                </div>
                {(selectedPackImpact.capabilities.length ||
                  selectedPackImpact.requires.length ||
                  selectedPackImpact.riskTags.length) ? (
                  <div className="mt-3 flex flex-wrap gap-2">
                    {selectedPackImpact.capabilities.map((cap) => (
                      <Badge key={`cap-${cap}`} variant="info">{cap}</Badge>
                    ))}
                    {selectedPackImpact.requires.map((req) => (
                      <Badge key={`req-${req}`} variant="warning">{req}</Badge>
                    ))}
                    {selectedPackImpact.riskTags.map((tag) => (
                      <Badge key={`risk-${tag}`} variant="danger">{tag}</Badge>
                    ))}
                  </div>
                ) : null}
              </div>
            ) : null}
            <div>
              <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Worker Health</div>
              {selectedPackWorkers.length ? (
                <div className="mt-2 space-y-2">
                  {selectedPackWorkers.map((worker, index) => (
                    <div key={`${worker.worker_id}-${index}`} className="rounded-2xl border border-border bg-white/70 p-3 text-xs text-muted">
                      <div className="flex items-center justify-between">
                        <div className="text-sm font-semibold text-ink">{worker.worker_id || "worker"}</div>
                        <Badge variant="info">{worker.pool || "default"}</Badge>
                      </div>
                      <div>Topic: {worker.topic || "-"}</div>
                      <div>CPU {formatPercent(worker.cpu_load)} · Mem {formatPercent(worker.memory_load)}</div>
                    </div>
                  ))}
                </div>
              ) : (
                <div className="mt-2 rounded-2xl border border-dashed border-border p-4 text-sm text-muted">
                  No workers matched to this pack’s topics yet.
                </div>
              )}
            </div>
            <div>
              <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Resources</div>
              <pre className="mt-2 rounded-2xl border border-border bg-white/70 p-3 text-[11px] text-ink">
                {JSON.stringify(selectedPack.resources || {}, null, 2)}
              </pre>
            </div>
            <div>
              <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Overlays</div>
              <pre className="mt-2 rounded-2xl border border-border bg-white/70 p-3 text-[11px] text-ink">
                {JSON.stringify(selectedPack.overlays || {}, null, 2)}
              </pre>
            </div>
            {verifyResults[selectedPack.id] ? (
              <div>
                <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Verification</div>
                <pre className="mt-2 rounded-2xl border border-border bg-white/70 p-3 text-[11px] text-ink">
                  {JSON.stringify(verifyResults[selectedPack.id], null, 2)}
                </pre>
              </div>
            ) : null}
          </div>
        ) : null}
      </Drawer>
    </div>
  );
}
