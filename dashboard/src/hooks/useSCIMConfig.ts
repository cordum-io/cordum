import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { get, post } from "@/api/client";

const SCIM_SETTINGS_PATH = "/scim/settings";
const SCIM_ROTATE_TOKEN_PATH = "/scim/settings/token";

export interface SCIMProvisionedUser {
  id: string;
  userName: string;
  displayName?: string;
  email?: string;
  source?: string;
  active: boolean;
  syncedAt?: string;
}

export interface SCIMConfigView {
  entitled: boolean;
  configured: boolean;
  endpointUrl: string;
  bearerToken?: string;
  bearerTokenMasked?: string;
  tokenManagedBy: string;
  users: SCIMProvisionedUser[];
}

export interface SCIMRotateTokenResponse {
  bearerToken: string;
  bearerTokenMasked?: string;
  tokenManagedBy: string;
}

async function fetchSCIMConfig(): Promise<SCIMConfigView> {
  return get<SCIMConfigView>(SCIM_SETTINGS_PATH);
}

async function rotateSCIMToken(): Promise<SCIMRotateTokenResponse> {
  return post<SCIMRotateTokenResponse>(SCIM_ROTATE_TOKEN_PATH);
}

export function useSCIMConfig(enabled = true) {
  return useQuery<SCIMConfigView, Error>({
    queryKey: ["scim-settings"],
    queryFn: fetchSCIMConfig,
    staleTime: 60_000,
    enabled,
  });
}

export function useRotateSCIMToken() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: rotateSCIMToken,
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["scim-settings"] });
    },
  });
}
