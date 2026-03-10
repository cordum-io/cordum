import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post } from "../api/client";
import { logger } from "../lib/logger";
import { useToastStore } from "../state/toast";
import type { Pack, ApiResponse, MarketplaceResponse } from "../api/types";
import {
  mapPackRecord,
  mapMarketplaceCatalog,
  mapMarketplaceItem,
  type BackendPackRecord,
  type BackendMarketplaceResponse,
} from "../api/transform";

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

export function usePacks() {
  return useQuery<ApiResponse<Pack[]>>({
    queryKey: ["packs"],
    queryFn: async () => {
      const res = await get<{ items: BackendPackRecord[] }>("/packs");
      return { items: (res.items ?? []).map(mapPackRecord) };
    },
    staleTime: 30_000,
  });
}

export function usePack(id: string) {
  return useQuery<Pack>({
    queryKey: ["pack", id],
    queryFn: async () => {
      const rec = await get<BackendPackRecord>(`/packs/${encodeURIComponent(id)}`);
      return mapPackRecord(rec);
    },
    enabled: !!id,
    staleTime: 30_000,
  });
}

export function useMarketplacePacks() {
  return useQuery<MarketplaceResponse>({
    queryKey: ["marketplace-packs"],
    queryFn: async () => {
      const res = await get<BackendMarketplaceResponse>("/marketplace/packs");
      return {
        catalogs: (res.catalogs ?? []).map(mapMarketplaceCatalog),
        items: (res.items ?? []).map(mapMarketplaceItem),
        fetched_at: res.fetched_at,
        cached: res.cached,
      };
    },
    staleTime: 60_000,
  });
}

// ---------------------------------------------------------------------------
// Mutations
// ---------------------------------------------------------------------------

interface InstallPackInput {
  catalogId: string;
  packId: string;
  version?: string;
  url?: string;
  sha256?: string;
  force?: boolean;
  upgrade?: boolean;
  inactive?: boolean;
}

export function useInstallPack() {
  const queryClient = useQueryClient();
  return useMutation<Pack, Error, InstallPackInput>({
    mutationFn: (input) => {
      logger.info("packs", "Installing pack", { packId: input.packId, catalogId: input.catalogId });
      return post<BackendPackRecord>("/marketplace/install", {
        catalog_id: input.catalogId,
        pack_id: input.packId,
        version: input.version,
        url: input.url,
        sha256: input.sha256,
        force: input.force,
        upgrade: input.upgrade,
        inactive: input.inactive,
      }).then(mapPackRecord);
    },
    onSuccess: (_, input) => {
      logger.info("packs", "Pack installed", { packId: input.packId });
      useToastStore.getState().addToast({ type: "success", title: "Pack installed" });
      queryClient.invalidateQueries({ queryKey: ["packs"] });
      queryClient.invalidateQueries({ queryKey: ["marketplace-packs"] });
    },
    onError: (err, input) => {
      logger.error("packs", "Pack install failed", { packId: input.packId, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Installation failed", description: err.message });
    },
  });
}

export function useUninstallPack() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => {
      logger.info("packs", "Uninstalling pack", { id });
      return post<void>(`/packs/${id}/uninstall`);
    },
    onSuccess: (_data, id) => {
      logger.info("packs", "Pack uninstalled", { id });
      useToastStore.getState().addToast({ type: "success", title: "Pack uninstalled" });
      queryClient.invalidateQueries({ queryKey: ["packs"] });
      queryClient.invalidateQueries({ queryKey: ["pack", id] });
      queryClient.invalidateQueries({ queryKey: ["marketplace-packs"] });
    },
    onError: (err, id) => {
      logger.error("packs", "Pack uninstall failed", { id, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to uninstall", description: err.message });
    },
  });
}

// ---------------------------------------------------------------------------
// Pack verification
// ---------------------------------------------------------------------------

export interface PackVerifyCheck {
  name: string;
  status: "pass" | "fail";
  message?: string;
  details?: string;
}

export interface PackVerifyResult {
  overall: "verified" | "failed";
  checks: PackVerifyCheck[];
  verified_at: string;
}

export function useVerifyPack() {
  return useMutation<PackVerifyResult, Error, string>({
    mutationFn: (packId) => {
      logger.info("packs", "Verifying pack", { packId });
      return post<PackVerifyResult>(`/packs/${packId}/verify`, {});
    },
    onSuccess: (result, packId) => {
      const msg = result.overall === "verified" ? "Pack verified" : "Pack verification failed";
      const type = result.overall === "verified" ? "success" as const : "warning" as const;
      logger.info("packs", msg, { packId });
      useToastStore.getState().addToast({ type, title: msg });
    },
    onError: (err, packId) => {
      logger.error("packs", "Pack verification error", { packId, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Verification failed", description: err.message });
    },
  });
}
