import { Input } from "../ui/Input";
import { Select } from "../ui/Select";
import { cn } from "../../lib/utils";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type JsonSchema = Record<string, unknown>;
type FormValue = Record<string, unknown>;

export interface SchemaFormProps {
  schema: JsonSchema;
  value: FormValue;
  onChange: (val: FormValue) => void;
  className?: string;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const MAX_DEPTH = 3;

function getDefault(prop: JsonSchema): unknown {
  if ("default" in prop) return prop.default;
  const type = prop.type as string | undefined;
  if (type === "boolean") return false;
  if (type === "number" || type === "integer") return undefined;
  if (type === "array") return [];
  if (type === "object") return {};
  return undefined;
}

function isRequired(schema: JsonSchema, key: string): boolean {
  const req = schema.required;
  return Array.isArray(req) && req.includes(key);
}

// ---------------------------------------------------------------------------
// Field renderers
// ---------------------------------------------------------------------------

function StringField({
  label,
  value,
  onChange,
  required,
  description,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  required?: boolean;
  description?: string;
}) {
  return (
    <div className="space-y-1">
      <label className="block text-xs font-medium text-ink">
        {label}
        {required && <span className="text-danger ml-0.5">*</span>}
      </label>
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        required={required}
        placeholder={description ?? ""}
      />
      {description && (
        <p className="text-[10px] text-muted-foreground">{description}</p>
      )}
    </div>
  );
}

function NumberField({
  label,
  value,
  onChange,
  required,
  description,
  min,
  max,
}: {
  label: string;
  value: number | undefined;
  onChange: (v: number | undefined) => void;
  required?: boolean;
  description?: string;
  min?: number;
  max?: number;
}) {
  return (
    <div className="space-y-1">
      <label className="block text-xs font-medium text-ink">
        {label}
        {required && <span className="text-danger ml-0.5">*</span>}
      </label>
      <Input
        type="number"
        value={value ?? ""}
        onChange={(e) => {
          const n = parseFloat(e.target.value);
          onChange(Number.isFinite(n) ? n : undefined);
        }}
        required={required}
        placeholder={description ?? ""}
        min={min}
        max={max}
      />
    </div>
  );
}

function BooleanField({
  label,
  value,
  onChange,
  description,
}: {
  label: string;
  value: boolean;
  onChange: (v: boolean) => void;
  description?: string;
}) {
  return (
    <div className="flex items-center gap-2">
      <input
        type="checkbox"
        checked={value}
        onChange={(e) => onChange(e.target.checked)}
        className="h-4 w-4 rounded border-border text-accent focus:ring-accent"
      />
      <label className="text-xs font-medium text-ink">{label}</label>
      {description && (
        <span className="text-[10px] text-muted-foreground">({description})</span>
      )}
    </div>
  );
}

function EnumField({
  label,
  value,
  options,
  onChange,
  required,
  description,
}: {
  label: string;
  value: string;
  options: string[];
  onChange: (v: string) => void;
  required?: boolean;
  description?: string;
}) {
  return (
    <div className="space-y-1">
      <label className="block text-xs font-medium text-ink">
        {label}
        {required && <span className="text-danger ml-0.5">*</span>}
      </label>
      <Select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        required={required}
      >
        <option value="">Select...</option>
        {options.map((opt) => (
          <option key={opt} value={opt}>
            {opt}
          </option>
        ))}
      </Select>
      {description && (
        <p className="text-[10px] text-muted-foreground">{description}</p>
      )}
    </div>
  );
}

function ArrayStringField({
  label,
  value,
  onChange,
  required,
  description,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  required?: boolean;
  description?: string;
}) {
  return (
    <div className="space-y-1">
      <label className="block text-xs font-medium text-ink">
        {label}
        {required && <span className="text-danger ml-0.5">*</span>}
      </label>
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        required={required}
        placeholder={description ?? "Comma-separated values"}
      />
      <p className="text-[10px] text-muted-foreground">
        {description ?? "Enter comma-separated values"}
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Recursive property renderer
// ---------------------------------------------------------------------------

function PropertyField({
  name,
  propSchema,
  parentSchema,
  value,
  onChange,
  depth,
}: {
  name: string;
  propSchema: JsonSchema;
  parentSchema: JsonSchema;
  value: unknown;
  onChange: (v: unknown) => void;
  depth: number;
}) {
  const type = propSchema.type as string | undefined;
  const required = isRequired(parentSchema, name);
  const description = propSchema.description as string | undefined;
  const label = (propSchema.title as string) ?? name;

  // Enum
  if (Array.isArray(propSchema.enum)) {
    return (
      <EnumField
        label={label}
        value={String(value ?? "")}
        options={propSchema.enum.map(String)}
        onChange={onChange}
        required={required}
        description={description}
      />
    );
  }

  // Boolean
  if (type === "boolean") {
    return (
      <BooleanField
        label={label}
        value={Boolean(value)}
        onChange={onChange}
        description={description}
      />
    );
  }

  // Number / Integer
  if (type === "number" || type === "integer") {
    return (
      <NumberField
        label={label}
        value={typeof value === "number" ? value : undefined}
        onChange={onChange}
        required={required}
        description={description}
        min={propSchema.minimum as number | undefined}
        max={propSchema.maximum as number | undefined}
      />
    );
  }

  // Array of strings
  if (type === "array") {
    const raw = Array.isArray(value) ? (value as string[]).join(", ") : String(value ?? "");
    return (
      <ArrayStringField
        label={label}
        value={raw}
        onChange={(v) =>
          onChange(
            v
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean),
          )
        }
        required={required}
        description={description}
      />
    );
  }

  // Nested object (depth limited)
  if (type === "object" && depth < MAX_DEPTH) {
    const objVal = (value && typeof value === "object" ? value : {}) as FormValue;
    return (
      <fieldset className="space-y-3 rounded-xl border border-border p-3">
        <legend className="px-1 text-xs font-semibold text-ink">
          {label}
          {required && <span className="text-danger ml-0.5">*</span>}
        </legend>
        <SchemaFormInner
          schema={propSchema}
          value={objVal}
          onChange={(v) => onChange(v)}
          depth={depth + 1}
        />
      </fieldset>
    );
  }

  // Default: string
  return (
    <StringField
      label={label}
      value={String(value ?? "")}
      onChange={onChange}
      required={required}
      description={description}
    />
  );
}

// ---------------------------------------------------------------------------
// Inner form (recursive)
// ---------------------------------------------------------------------------

function SchemaFormInner({
  schema,
  value,
  onChange,
  depth,
}: {
  schema: JsonSchema;
  value: FormValue;
  onChange: (val: FormValue) => void;
  depth: number;
}) {
  const properties = (schema.properties ?? {}) as Record<string, JsonSchema>;
  const keys = Object.keys(properties);

  if (keys.length === 0) return null;

  return (
    <div className="space-y-3">
      {keys.map((key) => {
        const prop = properties[key];
        return (
          <PropertyField
            key={key}
            name={key}
            propSchema={prop}
            parentSchema={schema}
            value={value[key] ?? getDefault(prop)}
            onChange={(v) => onChange({ ...value, [key]: v })}
            depth={depth}
          />
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Public component
// ---------------------------------------------------------------------------

export function SchemaForm({ schema, value, onChange, className }: SchemaFormProps) {
  return (
    <div className={cn("space-y-3", className)}>
      <SchemaFormInner schema={schema} value={value} onChange={onChange} depth={0} />
    </div>
  );
}
