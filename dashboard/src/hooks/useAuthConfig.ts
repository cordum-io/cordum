import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { AuthConfig } from "../types/api";

export function useAuthConfig() {
  return useQuery<AuthConfig | null>({
    queryKey: ["auth-config"],
    queryFn: async () => {
      try {
        return await api.getAuthConfig();
      } catch {
        return null;
      }
    },
    staleTime: 60_000,
  });
}
