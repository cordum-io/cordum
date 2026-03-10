import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post, del } from "../api/client";
import { logger } from "../lib/logger";
import { useToastStore } from "../state/toast";
import type { Schema, SchemaField, ApiResponse } from "../api/types";

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

export function useSchemas() {
  return useQuery<ApiResponse<Schema[]>>({
    queryKey: ["schemas"],
    queryFn: async () => {
      const res = await get<{ schemas: string[] }>("/schemas");
      const items = (res.schemas ?? []).map((id) => ({
        id,
        name: id,
        fields: [],
      }));
      return { items };
    },
    staleTime: 30_000,
  });
}

export function useSchema(id: string) {
  return useQuery<Schema>({
    queryKey: ["schema", id],
    queryFn: async () => {
      const res = await get<{ id: string; schema: Record<string, unknown> }>(`/schemas/${id}`);
      return {
        id: res.id,
        name: res.id,
        schema: res.schema,
        fields: parseJsonSchemaFields(res.schema),
      };
    },
    enabled: !!id,
    staleTime: 30_000,
  });
}

// ---------------------------------------------------------------------------
// Mutations
// ---------------------------------------------------------------------------

interface RegisterSchemaInput {
  id: string;
  schema: Record<string, unknown>;
}

export function useRegisterSchema() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, RegisterSchemaInput>({
    mutationFn: (input) => {
      logger.info("schemas", "Registering schema", { id: input.id });
      return post<void>("/schemas", input);
    },
    onSuccess: (_, input) => {
      logger.info("schemas", "Schema registered", { id: input.id });
      useToastStore.getState().addToast({ type: "success", title: "Schema registered" });
      queryClient.invalidateQueries({ queryKey: ["schemas"] });
    },
    onError: (err, input) => {
      logger.error("schemas", "Schema registration failed", { id: input.id, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Registration failed", description: err.message });
    },
  });
}

export function useDeleteSchema() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => {
      logger.info("schemas", "Deleting schema", { id });
      return del(`/schemas/${id}`);
    },
    onSuccess: (_, id) => {
      logger.info("schemas", "Schema deleted", { id });
      useToastStore.getState().addToast({ type: "success", title: "Schema deleted" });
      queryClient.invalidateQueries({ queryKey: ["schemas"] });
    },
    onError: (err, id) => {
      logger.error("schemas", "Schema delete failed", { id, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to delete schema", description: err.message });
    },
  });
}

function parseJsonSchemaFields(schema: Record<string, unknown>): SchemaField[] {
  if (!schema || typeof schema !== "object") return [];
  const properties =
    (schema as Record<string, unknown>).properties ?? schema;
  const required = new Set<string>(
    Array.isArray((schema as Record<string, unknown>).required)
      ? ((schema as Record<string, unknown>).required as string[])
      : [],
  );
  if (!properties || typeof properties !== "object") return [];
  return Object.entries(properties as Record<string, unknown>).map(([name, def]) => {
    const field: SchemaField = {
      name,
      type:
        typeof def === "object" && def !== null && "type" in def
          ? String((def as Record<string, unknown>).type)
          : "unknown",
      required: required.has(name),
    };
    if (typeof def === "object" && def !== null && "description" in def) {
      field.description = String((def as Record<string, unknown>).description);
    }
    return field;
  });
}

/** @internal exported for unit tests */
export const __schemasInternal = {
  parseJsonSchemaFields,
};
