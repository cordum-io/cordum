import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import { Textarea } from "../ui/Textarea";
import { useRegisterSchema } from "../../hooks/useSchemas";

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

const schemaFormSchema = z.object({
  id: z
    .string()
    .min(1, "ID is required")
    .max(200, "ID must be 200 characters or less")
    .regex(/^[a-zA-Z0-9._-]+$/, "ID must be alphanumeric with dots, dashes, or underscores"),
  body: z
    .string()
    .min(1, "Schema body is required")
    .refine(
      (val) => {
        try {
          const parsed = JSON.parse(val);
          return typeof parsed === "object" && parsed !== null;
        } catch {
          return false;
        }
      },
      { message: "Must be valid JSON object" },
    ),
});

type FormValues = z.infer<typeof schemaFormSchema>;

// ---------------------------------------------------------------------------
// Example JSON schema
// ---------------------------------------------------------------------------

const exampleSchema = JSON.stringify(
  {
    type: "object",
    properties: {
      prompt: { type: "string", description: "The user prompt" },
      maxTokens: { type: "number", description: "Max tokens to generate" },
    },
    required: ["prompt"],
  },
  null,
  2,
);

// ---------------------------------------------------------------------------
// SchemaRegisterForm
// ---------------------------------------------------------------------------

interface SchemaRegisterFormProps {
  onSuccess?: () => void;
  initialData?: { id: string; body: string };
}

export function SchemaRegisterForm({ onSuccess, initialData }: SchemaRegisterFormProps) {
  const registerSchema = useRegisterSchema();
  const isEdit = !!initialData?.id;

  const {
    register,
    handleSubmit,
    formState: { errors },
    reset,
  } = useForm<FormValues>({
    resolver: zodResolver(schemaFormSchema),
    defaultValues: {
      id: initialData?.id ?? "",
      body: initialData?.body ?? "",
    },
  });

  const onSubmit = (values: FormValues) => {
    const schemaObj = JSON.parse(values.body) as Record<string, unknown>;
    registerSchema.mutate(
      { id: values.id, schema: schemaObj },
      {
        onSuccess: () => {
          reset();
          onSuccess?.();
        },
      },
    );
  };

  return (
    <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
      {/* ID */}
      <div>
        <label className="mb-1 block text-xs font-semibold text-muted">
          Schema ID
        </label>
        <Input
          placeholder="e.g. job.payload.echo"
          readOnly={isEdit}
          className={isEdit ? "bg-surface2/50 text-muted cursor-not-allowed" : undefined}
          {...register("id")}
        />
        {errors.id && (
          <p className="mt-1 text-xs text-danger">{errors.id.message}</p>
        )}
      </div>

      {/* JSON Schema Body */}
      <div>
        <label className="mb-1 block text-xs font-semibold text-muted">
          JSON Schema Body
        </label>
        <Textarea
          rows={10}
          placeholder={exampleSchema}
          className="font-mono text-xs"
          {...register("body")}
        />
        {errors.body && (
          <p className="mt-1 text-xs text-danger">{errors.body.message}</p>
        )}
        <p className="mt-1 text-[11px] text-muted">
          Paste a JSON Schema object.
        </p>
      </div>

      {/* Submit */}
      {registerSchema.isError && (
        <p className="text-xs text-danger">
          Failed to {isEdit ? "update" : "register"} schema: {registerSchema.error.message}
        </p>
      )}

      <div className="flex justify-end">
        <Button type="submit" disabled={registerSchema.isPending}>
          {registerSchema.isPending
            ? isEdit ? "Updating..." : "Registering..."
            : isEdit ? "Update Schema" : "Register Schema"}
        </Button>
      </div>
    </form>
  );
}
