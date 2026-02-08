import { Badge } from "../ui/Badge";
import { Card } from "../ui/Card";
import type { Schema, SchemaField } from "../../api/types";

// ---------------------------------------------------------------------------
// Field row
// ---------------------------------------------------------------------------

function FieldRow({ field, depth = 0 }: { field: SchemaField; depth?: number }) {
  return (
    <tr className="border-b border-border last:border-b-0">
      <td className="px-4 py-2.5 font-mono text-xs text-ink" style={{ paddingLeft: `${16 + depth * 20}px` }}>
        {field.name}
      </td>
      <td className="px-4 py-2.5">
        <Badge variant="info" className="text-[10px]">
          {field.type}
        </Badge>
      </td>
      <td className="px-4 py-2.5 text-center">
        {field.required ? (
          <Badge variant="danger" className="text-[10px]">required</Badge>
        ) : (
          <span className="text-xs text-muted">optional</span>
        )}
      </td>
      <td className="px-4 py-2.5 text-xs text-muted">
        {field.description || "—"}
      </td>
    </tr>
  );
}

// ---------------------------------------------------------------------------
// SchemaViewer
// ---------------------------------------------------------------------------

export function SchemaViewer({ schema }: { schema: Schema }) {
  const fields = schema.fields ?? [];
  return (
    <div className="space-y-4">
      {/* Header info */}
      <Card>
        <div className="flex items-center justify-between">
          <div>
            <h3 className="font-display text-lg font-semibold text-ink">
              {schema.id}
            </h3>
            <p className="mt-1 text-xs text-muted">
              {fields.length} field{fields.length !== 1 ? "s" : ""} &middot; Schema registry
            </p>
          </div>
          <Badge variant="info">JSON</Badge>
        </div>
      </Card>

      {/* Fields table */}
      {fields.length === 0 ? (
        <Card className="border-dashed text-center text-sm text-muted py-8">
          No fields parsed from this schema.
        </Card>
      ) : (
        <div className="overflow-hidden rounded-2xl border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-surface2/50">
                <th className="px-4 py-2.5 text-left text-xs font-semibold uppercase tracking-wide text-muted">
                  Field
                </th>
                <th className="px-4 py-2.5 text-left text-xs font-semibold uppercase tracking-wide text-muted">
                  Type
                </th>
                <th className="px-4 py-2.5 text-center text-xs font-semibold uppercase tracking-wide text-muted">
                  Required
                </th>
                <th className="px-4 py-2.5 text-left text-xs font-semibold uppercase tracking-wide text-muted">
                  Description
                </th>
              </tr>
            </thead>
            <tbody>
              {fields.map((field) => (
                <FieldRow key={field.name} field={field} />
              ))}
            </tbody>
          </table>
        </div>
      )}

      {schema.schema && (
        <Card>
          <h4 className="text-xs font-semibold uppercase tracking-wider text-muted">
            Raw Schema
          </h4>
          <pre className="mt-2 max-h-96 overflow-auto rounded-xl bg-surface2 p-3 text-xs text-ink font-mono">
            {JSON.stringify(schema.schema, null, 2)}
          </pre>
        </Card>
      )}
    </div>
  );
}
