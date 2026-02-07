import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post } from "../api/client";
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
      const rec = await get<BackendPackRecord>(`/packs/${id}`);
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
    mutationFn: (input) =>
      post<BackendPackRecord>("/marketplace/install", {
        catalog_id: input.catalogId,
        pack_id: input.packId,
        version: input.version,
        url: input.url,
        sha256: input.sha256,
        force: input.force,
        upgrade: input.upgrade,
        inactive: input.inactive,
      }).then(mapPackRecord),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["packs"] });
      queryClient.invalidateQueries({ queryKey: ["marketplace-packs"] });
    },
  });
}

export function useUninstallPack() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => post<void>(`/packs/${id}/uninstall`),
    onSuccess: (_data, id) => {
      queryClient.invalidateQueries({ queryKey: ["packs"] });
      queryClient.invalidateQueries({ queryKey: ["pack", id] });
      queryClient.invalidateQueries({ queryKey: ["marketplace-packs"] });
    },
  });
}
