import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post, del } from "../api/client";
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
    mutationFn: (input) => post<void>("/schemas", input),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["schemas"] });
    },
  });
}

export function useDeleteSchema() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => del(`/schemas/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["schemas"] });
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
