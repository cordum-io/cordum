import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import type { AdminLock } from "../api/types";

interface AdminLocksResponse {
  locks: AdminLock[];
}

export function useAdminLocks() {
  return useQuery<AdminLocksResponse>({
    queryKey: ["admin-locks"],
    queryFn: () => get<AdminLocksResponse>("/admin/locks"),
    refetchInterval: 5_000,
    staleTime: 3_000,
  });
}
