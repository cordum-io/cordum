/*
 * DESIGN: "Control Surface" — Schema Detail
 * PRD Section 22: Schema version history and field definitions
 */
import { useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  useForm,
  useFieldArray,
  type FieldErrors,
  type FieldError,
} from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { motion } from "framer-motion";
import { get } from "@/api/client";
import { useRegisterSchema } from "@/hooks/useSchemas";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { ArrowLeft, FileJson, Copy, Clock, Hash, Edit, Plus, Trash2 } from "lucide-react";
import { cn, formatRelativeTime } from "@/lib/utils";
import { toast } from "sonner";
import { CodeBlock } from "@/components/ui/CodeBlock";
import { ErrorBanner } from "@/components/ui/ErrorBanner";

const FIELD_TYPES = ["string", "number", "boolean", "array", "object", "integer"] as const;
const SCHEMA_TYPES = ["input", "output", "config"] as const;

const createSchemaFormSchema = z.object({
  id: z.string().min(1, "Schema name is required").regex(/^[a-z0-9][a-z0-9._-]*$/, "Lowercase alphanumeric, dots, hyphens, underscores only"),
  type: z.enum(SCHEMA_TYPES),
  description: z.string().optional(),
  fields: z.array(z.object({
    name: z.string().min(1, "Field name is required"),
    type: z.enum(FIELD_TYPES),
    required: z.boolean(),
    description: z.string().optional(),
  })).min(1, "At least one field is required"),
});

type CreateSchemaForm = z.infer<typeof createSchemaFormSchema>;

interface SchemaField {
  name: string;
  type: string;
  required: boolean;
  description?: string;
}

interface SchemaVersion {
  version: string;
  createdAt: string;
  fields: SchemaField[];
  changelog?: string;
}

function readFieldErrorMessage(error?: FieldError): string | null {
  return typeof error?.message === "string" ? error.message : null;
}

export function getSchemaCreateErrorMessages(
  errors: FieldErrors<CreateSchemaForm>,
): string[] {
  const messages = new Set<string>();

  const push = (value?: string | null) => {
    if (value) messages.add(value);
  };

  push(readFieldErrorMessage(errors.id));
  push(readFieldErrorMessage(errors.type));
  push(readFieldErrorMessage(errors.description));
  push(readFieldErrorMessage(errors.fields as FieldError | undefined));
  push(readFieldErrorMessage(errors.fields?.root));

  if (Array.isArray(errors.fields)) {
    errors.fields.forEach((fieldError) => {
      if (!fieldError) return;
      push(readFieldErrorMessage(fieldError.name));
      push(readFieldErrorMessage(fieldError.type));
      push(readFieldErrorMessage(fieldError.required));
      push(readFieldErrorMessage(fieldError.description));
    });
  }

  return Array.from(messages);
}

export function SchemaCreateForm() {
  const navigate = useNavigate();
  const registerSchema = useRegisterSchema();
  const { register, control, handleSubmit, formState: { errors, isSubmitting } } = useForm<CreateSchemaForm>({
    resolver: zodResolver(createSchemaFormSchema),
    defaultValues: {
      id: "",
      type: "input",
      description: "",
      fields: [{ name: "", type: "string", required: false, description: "" }],
    },
  });
  const { fields, append, remove } = useFieldArray({ control, name: "fields" });
  const errorMessages = getSchemaCreateErrorMessages(errors);

  function onSubmit(data: CreateSchemaForm) {
    const properties: Record<string, unknown> = {};
    const required: string[] = [];
    for (const f of data.fields) {
      properties[f.name] = {
        type: f.type,
        ...(f.description ? { description: f.description } : {}),
      };
      if (f.required) required.push(f.name);
    }
    const schema: Record<string, unknown> = {
      type: "object",
      properties,
      ...(required.length > 0 ? { required } : {}),
      ...(data.description ? { description: data.description } : {}),
    };

    registerSchema.mutate({ id: data.id, schema }, {
      onSuccess: () => navigate(`/schemas/${encodeURIComponent(data.id)}`),
    });
  }

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <div className="flex items-center gap-3">
        <button type="button" onClick={() => navigate("/schemas")} className="p-1.5 rounded-full hover:bg-surface-2 transition-colors">
          <ArrowLeft className="w-4 h-4 text-muted-foreground" />
        </button>
        <FileJson className="w-5 h-5 text-cordum" />
        <div>
          <h1 className="text-lg font-display font-bold text-foreground">New Schema</h1>
          <p className="text-xs text-muted-foreground">Define a new schema for your platform</p>
        </div>
      </div>

      <form onSubmit={handleSubmit(onSubmit)} className="space-y-6">
        {errorMessages.length > 0 && (
          <div className="rounded-2xl border border-destructive/20 bg-destructive/5 p-4">
            <p className="text-xs font-mono uppercase tracking-wide text-destructive">
              Validation issues
            </p>
            <ul className="mt-2 space-y-1 text-sm text-destructive">
              {errorMessages.map((message) => (
                <li key={message}>{message}</li>
              ))}
            </ul>
          </div>
        )}

        <div className="instrument-card p-6 space-y-4">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="block text-xs font-medium text-muted-foreground mb-1">Schema ID</label>
              <input {...register("id")} placeholder="e.g. job-input-schema" className="w-full rounded-xl border border-border bg-surface-0 px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-cordum/30" />
              {errors.id && <p className="mt-1 text-xs text-destructive">{errors.id.message}</p>}
            </div>
            <div>
              <label className="block text-xs font-medium text-muted-foreground mb-1">Type</label>
              <select {...register("type")} className="w-full rounded-xl border border-border bg-surface-0 px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-cordum/30">
                {SCHEMA_TYPES.map(t => <option key={t} value={t}>{t}</option>)}
              </select>
              {errors.type && <p className="mt-1 text-xs text-destructive">{errors.type.message}</p>}
            </div>
          </div>
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">Description</label>
            <input {...register("description")} placeholder="Optional description" className="w-full rounded-xl border border-border bg-surface-0 px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-cordum/30" />
            {errors.description && <p className="mt-1 text-xs text-destructive">{errors.description.message}</p>}
          </div>
        </div>

        <div className="instrument-card p-6 space-y-4">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold text-foreground">Fields</h2>
            <button type="button" onClick={() => append({ name: "", type: "string", required: false, description: "" })} className="flex items-center gap-1 text-xs text-cordum hover:text-cordum/80 transition-colors">
              <Plus className="w-3.5 h-3.5" />Add Field
            </button>
          </div>
          {(errors.fields?.root || readFieldErrorMessage(errors.fields as FieldError | undefined)) && (
            <p className="text-xs text-destructive">
              {errors.fields?.root?.message ?? readFieldErrorMessage(errors.fields as FieldError | undefined)}
            </p>
          )}

          <div className="space-y-3">
            {fields.map((field, index) => (
              <div key={field.id} className="grid grid-cols-[1fr_auto_auto_1fr_auto] items-start gap-2 p-3 rounded-xl bg-surface-1">
                <div>
                  <input {...register(`fields.${index}.name`)} placeholder="Field name" className="w-full rounded-lg border border-border bg-surface-0 px-2.5 py-1.5 text-xs text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-cordum/30" />
                  {errors.fields?.[index]?.name && <p className="mt-0.5 text-xs text-destructive">{errors.fields[index].name?.message}</p>}
                </div>
                <div>
                  <select {...register(`fields.${index}.type`)} className="rounded-lg border border-border bg-surface-0 px-2 py-1.5 text-xs text-foreground focus:outline-none focus:ring-2 focus:ring-cordum/30">
                    {FIELD_TYPES.map(t => <option key={t} value={t}>{t}</option>)}
                  </select>
                  {errors.fields?.[index]?.type && <p className="mt-0.5 text-xs text-destructive">{errors.fields[index].type?.message}</p>}
                </div>
                <label className="flex items-center gap-1.5 text-xs text-muted-foreground whitespace-nowrap py-1.5">
                  <input type="checkbox" {...register(`fields.${index}.required`)} className="rounded border-border" />
                  Required
                </label>
                <div>
                  <input {...register(`fields.${index}.description`)} placeholder="Description" className="w-full rounded-lg border border-border bg-surface-0 px-2.5 py-1.5 text-xs text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-cordum/30" />
                  {errors.fields?.[index]?.description && <p className="mt-0.5 text-xs text-destructive">{errors.fields[index].description?.message}</p>}
                </div>
                <button type="button" onClick={() => fields.length > 1 && remove(index)} disabled={fields.length <= 1} className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors disabled:opacity-30 disabled:cursor-not-allowed">
                  <Trash2 className="w-3.5 h-3.5" />
                </button>
              </div>
            ))}
          </div>
        </div>

        <div className="flex items-center gap-3">
          <Button type="submit" disabled={isSubmitting || registerSchema.isPending}>
            {registerSchema.isPending ? "Creating…" : "Create Schema"}
          </Button>
          <Button type="button" variant="outline" onClick={() => navigate("/schemas")}>Cancel</Button>
        </div>
      </form>
    </motion.div>
  );
}

export default function SchemaDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const [activeTab, setActiveTab] = useState("fields");

  const isCreateMode = id === "new";

  const { data: schema, isLoading, isError, error, refetch } = useQuery({
    queryKey: ["schema", id],
    queryFn: async () => {
      const res = await get<{ id: string; schema: Record<string, unknown> }>(`/schemas/${encodeURIComponent(id!)}`);
      const raw = res.schema ?? {};
      const properties = (raw.properties ?? {}) as Record<string, Record<string, unknown>>;
      const requiredSet = new Set<string>(Array.isArray(raw.required) ? raw.required as string[] : []);
      const fields: SchemaField[] = Object.entries(properties).map(([name, def]) => ({
        name,
        type: typeof def?.type === "string" ? def.type : Array.isArray(def?.type) ? def.type.join(" | ") : "unknown",
        required: requiredSet.has(name),
        description: typeof def?.description === "string" ? def.description : undefined,
      }));
      return {
        id: res.id,
        name: res.id,
        type: typeof raw.type === "string" ? raw.type : "object",
        currentVersion: "1",
        versions: [{ version: "1", createdAt: new Date().toISOString(), fields }] as SchemaVersion[],
        schema: raw,
      };
    },
    enabled: !isCreateMode,
  });

  const tabs = ["fields", "versions", "json"];
  const currentVersion = schema?.versions?.find(v => v.version === schema.currentVersion) || schema?.versions?.[0];

  if (isCreateMode) {
    return <SchemaCreateForm />;
  }

  if (isError) {
    return <ErrorBanner message={error instanceof Error ? error.message : "Failed to load schema"} onRetry={() => void refetch()} />;
  }

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div className="flex items-center gap-3">
          <div className="h-8 w-8 rounded bg-surface-2 animate-pulse" />
          <div className="h-6 w-48 rounded bg-surface-2 animate-pulse" />
        </div>
        {Array.from({ length: 3 }).map((_, i) => <SkeletonCard key={i} />)}
      </div>
    );
  }

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <button type="button" onClick={() => navigate("/schemas")} className="p-1.5 rounded-full hover:bg-surface-2 transition-colors">
            <ArrowLeft className="w-4 h-4 text-muted-foreground" />
          </button>
          <FileJson className="w-5 h-5 text-cordum" />
          <div>
            <h1 className="text-lg font-display font-bold text-foreground">{schema?.name || id}</h1>
            <div className="flex items-center gap-2 mt-0.5">
              <StatusBadge variant="info">{schema?.type}</StatusBadge>
              <span className="text-xs font-mono font-medium text-muted-foreground">v{schema?.currentVersion}</span>
            </div>
          </div>
        </div>
        <Button variant="outline" size="sm" disabled title="Schema editing not yet available">
          <Edit className="w-3 h-3 mr-1" />Edit Schema
        </Button>
      </div>

      {/* Tabs */}
      <div className="flex items-center gap-1 p-1 rounded-2xl bg-surface-1 w-fit">
        {tabs.map(tab => (
          <button type="button"
            key={tab}
            onClick={() => setActiveTab(tab)}
            className={cn(
              "px-4 py-1.5 text-xs font-medium rounded-2xl transition-colors capitalize",
              activeTab === tab ? "bg-cordum/10 text-cordum" : "text-muted-foreground hover:text-foreground",
            )}
          >
            {tab === "json" ? "JSON" : tab}
          </button>
        ))}
      </div>

      {/* Fields Tab */}
      {activeTab === "fields" && currentVersion && (
        <div className="instrument-card overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-surface-0">
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Field</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Type</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Required</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Description</th>
              </tr>
            </thead>
            <tbody>
              {(currentVersion.fields || []).map((field, i) => (
                <tr key={field.name} className="border-b border-border last:border-0 hover:bg-surface-1 transition-colors">
                  <td className="px-5 py-3 font-mono text-xs text-foreground">{field.name}</td>
                  <td className="px-5 py-3"><StatusBadge variant="info">{field.type}</StatusBadge></td>
                  <td className="px-5 py-3">{field.required ? <StatusBadge variant="warning">required</StatusBadge> : <span className="text-xs text-muted-foreground">optional</span>}</td>
                  <td className="px-5 py-3 text-xs text-muted-foreground">{field.description || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Versions Tab */}
      {activeTab === "versions" && (
        <div className="space-y-3">
          {(schema?.versions || []).map((v, i) => (
            <motion.div
              key={v.version}
              initial={{ opacity: 0, y: 8 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: i * 0.05 }}
              className={cn("instrument-card p-4", v.version === schema?.currentVersion && "status-healthy")}
            >
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <Hash className="w-3.5 h-3.5 text-cordum" />
                  <span className="text-sm font-mono font-semibold text-foreground">v{v.version}</span>
                  {v.version === schema?.currentVersion && <StatusBadge variant="healthy">current</StatusBadge>}
                </div>
                <span className="text-xs text-muted-foreground flex items-center gap-1">
                  <Clock className="w-3 h-3" />{formatRelativeTime(v.createdAt)}
                </span>
              </div>
              {v.changelog && <p className="text-xs text-muted-foreground mt-2">{v.changelog}</p>}
              <p className="text-xs text-muted-foreground mt-1">{v.fields.length} fields</p>
            </motion.div>
          ))}
        </div>
      )}

      {/* JSON Tab */}
      {activeTab === "json" && schema?.schema && (
        <CodeBlock title={schema?.name ?? "JSON Schema"} language="json" copyable maxHeight={384}>{JSON.stringify(schema.schema, null, 2)}</CodeBlock>
      )}
    </motion.div>
  );
}
