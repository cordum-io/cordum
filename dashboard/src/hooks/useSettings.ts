import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post, del, put } from "../api/client";
import type { ApiKey, User, ApiResponse, AuthConfig } from "../api/types";

// ---------------------------------------------------------------------------
// System config
// ---------------------------------------------------------------------------

export interface SystemConfig {
  [key: string]: unknown;
}

export function useConfig() {
  return useQuery<SystemConfig>({
    queryKey: ["config"],
    queryFn: () => get<SystemConfig>("/config"),
    staleTime: 60_000,
  });
}

export function useSetConfig() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, Partial<SystemConfig>>({
    mutationFn: (patch) => post<void>("/config", patch),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["config"] });
    },
  });
}

// ---------------------------------------------------------------------------
// Auth config (re-export for consistency)
// ---------------------------------------------------------------------------

export { useAuthConfig } from "./useAuthConfig";

export function useAuthConfigAdmin() {
  return useQuery<AuthConfig>({
    queryKey: ["auth-config-admin"],
    queryFn: () => get<AuthConfig>("/auth/config"),
    staleTime: 60_000,
  });
}

// ---------------------------------------------------------------------------
// API keys
// ---------------------------------------------------------------------------

export function useApiKeys() {
  return useQuery<ApiResponse<ApiKey[]>>({
    queryKey: ["api-keys"],
    queryFn: () => get<ApiResponse<ApiKey[]>>("/auth/keys"),
    staleTime: 30_000,
  });
}

interface CreateApiKeyInput {
  name: string;
  scopes: string[];
}

interface CreateApiKeyResponse {
  key: ApiKey;
  secret: string;
}

export function useCreateApiKey() {
  const queryClient = useQueryClient();
  return useMutation<CreateApiKeyResponse, Error, CreateApiKeyInput>({
    mutationFn: (input) => post<CreateApiKeyResponse>("/auth/keys", input),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["api-keys"] });
    },
  });
}

export function useRevokeApiKey() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => del(`/auth/keys/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["api-keys"] });
    },
  });
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

export function useUsers() {
  return useQuery<ApiResponse<User[]>>({
    queryKey: ["users"],
    queryFn: () => get<ApiResponse<User[]>>("/users"),
    staleTime: 30_000,
  });
}

interface CreateUserInput {
  username: string;
  password: string;
  role: string;
}

export function useCreateUser() {
  const queryClient = useQueryClient();
  return useMutation<User, Error, CreateUserInput>({
    mutationFn: (input) => post<User>("/users", input),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
    },
  });
}

interface UpdateUserInput {
  id: string;
  data: Partial<Pick<User, "email" | "display_name" | "roles">>;
}

export function useUpdateUser() {
  const queryClient = useQueryClient();
  return useMutation<User, Error, UpdateUserInput>({
    mutationFn: ({ id, data }) => put<User>(`/users/${id}`, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
    },
  });
}

export function useDeleteUser() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => del(`/users/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
    },
  });
}
