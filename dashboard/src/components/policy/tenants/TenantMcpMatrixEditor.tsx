import type { TenantMcpMatrix } from "./TenantMcpGovernanceSection";
import { TenantTagListEditor } from "./TenantTagListEditor";

interface TenantMcpMatrixEditorProps {
  matrix: TenantMcpMatrix;
  readOnly?: boolean;
  onChange: (next: TenantMcpMatrix) => void;
}

function updateMatrixList(
  matrix: TenantMcpMatrix,
  key: keyof TenantMcpMatrix,
  values: string[],
): TenantMcpMatrix {
  return {
    ...matrix,
    [key]: values,
  };
}

export function TenantMcpMatrixEditor({
  matrix,
  readOnly = false,
  onChange,
}: TenantMcpMatrixEditorProps) {
  return (
    <section className="space-y-3">
      <div className="rounded border border-border/70 bg-surface-1 p-3 text-xs text-muted-foreground">
        <p>
          MCP matrix precedence:{" "}
          <span className="font-medium text-foreground">deny overrides allow</span>{" "}
          for matching server/tool/resource/action patterns.
        </p>
        <p className="mt-1 text-[11px]">
          Entries are normalized for duplicate removal with case-insensitive matching.
        </p>
      </div>

      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <TenantTagListEditor
          inputId="tenant-mcp-allow-servers"
          label="Allow MCP servers"
          helpText="Comma-separated server patterns allowed for this tenant."
          values={matrix.allowServers}
          readOnly={readOnly}
          onChange={(next) => onChange(updateMatrixList(matrix, "allowServers", next))}
        />
        <TenantTagListEditor
          inputId="tenant-mcp-deny-servers"
          label="Deny MCP servers"
          helpText="Comma-separated server patterns denied for this tenant."
          values={matrix.denyServers}
          readOnly={readOnly}
          onChange={(next) => onChange(updateMatrixList(matrix, "denyServers", next))}
        />

        <TenantTagListEditor
          inputId="tenant-mcp-allow-tools"
          label="Allow MCP tools"
          helpText="Comma-separated tool patterns allowed for this tenant."
          values={matrix.allowTools}
          readOnly={readOnly}
          onChange={(next) => onChange(updateMatrixList(matrix, "allowTools", next))}
        />
        <TenantTagListEditor
          inputId="tenant-mcp-deny-tools"
          label="Deny MCP tools"
          helpText="Comma-separated tool patterns denied for this tenant."
          values={matrix.denyTools}
          readOnly={readOnly}
          onChange={(next) => onChange(updateMatrixList(matrix, "denyTools", next))}
        />

        <TenantTagListEditor
          inputId="tenant-mcp-allow-resources"
          label="Allow MCP resources"
          helpText="Comma-separated resource patterns allowed for this tenant."
          values={matrix.allowResources}
          readOnly={readOnly}
          onChange={(next) => onChange(updateMatrixList(matrix, "allowResources", next))}
        />
        <TenantTagListEditor
          inputId="tenant-mcp-deny-resources"
          label="Deny MCP resources"
          helpText="Comma-separated resource patterns denied for this tenant."
          values={matrix.denyResources}
          readOnly={readOnly}
          onChange={(next) => onChange(updateMatrixList(matrix, "denyResources", next))}
        />

        <TenantTagListEditor
          inputId="tenant-mcp-allow-actions"
          label="Allow MCP actions"
          helpText="Comma-separated action patterns allowed for this tenant."
          values={matrix.allowActions}
          readOnly={readOnly}
          onChange={(next) => onChange(updateMatrixList(matrix, "allowActions", next))}
        />
        <TenantTagListEditor
          inputId="tenant-mcp-deny-actions"
          label="Deny MCP actions"
          helpText="Comma-separated action patterns denied for this tenant."
          values={matrix.denyActions}
          readOnly={readOnly}
          onChange={(next) => onChange(updateMatrixList(matrix, "denyActions", next))}
        />
      </div>
    </section>
  );
}
