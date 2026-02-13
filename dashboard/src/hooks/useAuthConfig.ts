import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import type { AuthConfig } from "../api/types";

async function fetchAuthConfig(): Promise<AuthConfig> {
  return get<AuthConfig>("/auth/config");
}

export function useAuthConfig() {
  return useQuery<AuthConfig>({
    queryKey: ["auth-config"],
    queryFn: fetchAuthConfig,
    staleTime: 5 * 60_000,
    retry: 1,
  });
}
