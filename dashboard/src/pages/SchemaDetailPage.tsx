import { useParams, useNavigate } from "react-router-dom";
import { ArrowLeft, Loader } from "lucide-react";
import { Button } from "../components/ui/Button";
import { SchemaViewer } from "../components/schemas/SchemaViewer";
import { useSchema } from "../hooks/useSchemas";
import { usePageTitle } from "../hooks/usePageTitle";
import { isValidResourceId } from "../lib/utils";

export default function SchemaDetailPage() {
  const { id: rawId } = useParams<{ id: string }>();
  const id = isValidResourceId(rawId) ? rawId : undefined;
  usePageTitle(id ? `Schema ${id.slice(0, 8)}` : "Schema");
  const navigate = useNavigate();
  const { data: schema, isLoading, isError } = useSchema(id ?? "");

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="sm" onClick={() => navigate("/schemas")}>
          <ArrowLeft className="h-4 w-4" />
        </Button>
        <h1 className="font-display text-2xl font-bold text-ink">
          Schema Detail
        </h1>
      </div>

      {isLoading && (
        <div className="flex items-center justify-center py-16 text-sm text-muted">
          <Loader className="mr-2 h-4 w-4 animate-spin" />
          Loading schema...
        </div>
      )}

      {isError && (
        <p className="text-sm text-danger">Failed to load schema.</p>
      )}

      {schema && <SchemaViewer schema={schema} />}
    </div>
  );
}
