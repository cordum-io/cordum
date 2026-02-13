import { useState, useCallback } from "react";
import { Database, Pencil, Plus, Trash2, X, Loader } from "lucide-react";
import { Button } from "../components/ui/Button";
import { Drawer } from "../components/ui/Drawer";
import { SchemaRegisterForm } from "../components/schemas/SchemaRegisterForm";
import { SchemaViewer } from "../components/schemas/SchemaViewer";
import { useSchemas, useSchema, useDeleteSchema } from "../hooks/useSchemas";
import type { Schema } from "../api/types";
import { usePageTitle } from "../hooks/usePageTitle";

// ---------------------------------------------------------------------------
// Confirm dialog
// ---------------------------------------------------------------------------

function ConfirmDialog({
  schemaId,
  isPending,
  onConfirm,
  onCancel,
}: {
  schemaId: string;
  isPending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="surface-card w-full max-w-md rounded-3xl p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h3 className="font-display text-lg font-semibold text-ink">
            Delete Schema
          </h3>
          <button
            onClick={onCancel}
            className="rounded-full p-1 hover:bg-surface2"
          >
            <X className="h-4 w-4 text-muted" />
          </button>
        </div>

        <p className="mb-6 text-sm text-muted">
          Are you sure you want to delete{" "}
          <strong className="text-ink">{schemaId}</strong>? This action cannot be undone.
        </p>

        <div className="flex justify-end gap-3">
          <Button
            variant="ghost"
            size="sm"
            onClick={onCancel}
            disabled={isPending}
          >
            Cancel
          </Button>
          <Button
            variant="danger"
            size="sm"
            onClick={onConfirm}
            disabled={isPending}
          >
            {isPending ? "Deleting..." : "Delete"}
          </Button>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// SchemasPage
// ---------------------------------------------------------------------------

export default function SchemasPage() {
  usePageTitle("Schemas");
  const { data, isLoading, error } = useSchemas();
  const deleteMutation = useDeleteSchema();
  const [confirmSchemaId, setConfirmSchemaId] = useState<string | null>(null);
  const [registerOpen, setRegisterOpen] = useState(false);
  const [selectedSchemaId, setSelectedSchemaId] = useState<string | null>(null);
  const [editSchemaId, setEditSchemaId] = useState<string | null>(null);
  const { data: selectedSchema } = useSchema(selectedSchemaId ?? "");
  const { data: editSchema } = useSchema(editSchemaId ?? "");

  const schemas = data?.items ?? [];

  const handleDelete = useCallback(() => {
    if (!confirmSchemaId) return;
    deleteMutation.mutate(confirmSchemaId, {
      onSuccess: () => setConfirmSchemaId(null),
    });
  }, [confirmSchemaId, deleteMutation]);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="font-display text-2xl font-bold text-ink">Schemas</h1>
          <p className="text-sm text-muted">
            Data contracts for job payloads.
          </p>
        </div>
        <Button size="sm" onClick={() => setRegisterOpen(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" />
          Register Schema
        </Button>
      </div>

      {isLoading && (
        <div className="flex items-center justify-center py-16 text-sm text-muted">
          <Loader className="mr-2 h-4 w-4 animate-spin" />
          Loading schemas...
        </div>
      )}

      {error && (
        <p className="text-sm text-danger">
          Failed to load schemas:{" "}
          {error instanceof Error ? error.message : "Unknown error"}
        </p>
      )}

      {!isLoading && !error && schemas.length === 0 && (
        <div className="flex flex-col items-center justify-center rounded-3xl border border-dashed border-border py-16 text-center">
          <Database className="mb-3 h-10 w-10 text-muted" />
          <p className="text-sm text-muted">No schemas registered</p>
        </div>
      )}

      {schemas.length > 0 && (
        <div className="overflow-hidden rounded-2xl border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-surface2/50">
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-muted">
                  ID
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-muted">
                  Fields
                </th>
                <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wide text-muted">
                  Actions
                </th>
              </tr>
            </thead>
            <tbody>
              {schemas.map((schema) => (
                <tr
                  key={schema.id}
                  className="cursor-pointer border-b border-border last:border-b-0 hover:bg-surface2/30 transition-colors"
                  onClick={() => setSelectedSchemaId(schema.id)}
                >
                  <td className="px-4 py-3 font-medium text-ink">
                    {schema.id}
                  </td>
                  <td className="px-4 py-3 text-muted">
                    {schema.fields?.length ? `${schema.fields.length} field${schema.fields.length !== 1 ? "s" : ""}` : "—"}
                  </td>
                  <td className="px-4 py-3 text-right">
                    <div className="flex items-center justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={(e) => {
                          e.stopPropagation();
                          setEditSchemaId(schema.id);
                        }}
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-danger hover:bg-danger/10"
                        onClick={(e) => {
                          e.stopPropagation();
                          setConfirmSchemaId(schema.id);
                        }}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {confirmSchemaId && (
        <ConfirmDialog
          schemaId={confirmSchemaId}
          isPending={deleteMutation.isPending}
          onConfirm={handleDelete}
          onCancel={() => setConfirmSchemaId(null)}
        />
      )}

      <Drawer open={registerOpen} onClose={() => setRegisterOpen(false)} size="md">
        <h2 className="mb-4 font-display text-lg font-semibold text-ink">
          Register New Schema
        </h2>
        <SchemaRegisterForm onSuccess={() => setRegisterOpen(false)} />
      </Drawer>

      <Drawer open={!!selectedSchemaId} onClose={() => setSelectedSchemaId(null)} size="lg">
        {selectedSchemaId && selectedSchema && <SchemaViewer schema={selectedSchema} />}
      </Drawer>

      <Drawer open={!!editSchemaId} onClose={() => setEditSchemaId(null)} size="md">
        <h2 className="mb-4 font-display text-lg font-semibold text-ink">
          Edit Schema
        </h2>
        {editSchemaId && editSchema && (
          <SchemaRegisterForm
            initialData={{
              id: editSchema.id,
              body: editSchema.schema
                ? JSON.stringify(editSchema.schema, null, 2)
                : "",
            }}
            onSuccess={() => setEditSchemaId(null)}
          />
        )}
      </Drawer>
    </div>
  );
}
