import {
  cloneElement,
  isValidElement,
  type ReactElement,
  type ReactNode,
  useId,
} from "react";
import { CircleHelp } from "lucide-react";
import { cn } from "@/lib/utils";

interface FieldHelpProps {
  label: string;
  helpText: string;
  hint?: string;
  required?: boolean;
  inputId?: string;
  className?: string;
  error?: string | null;
  children?: ReactNode;
}

function joinDescribedBy(values: Array<string | null | undefined>): string | undefined {
  const normalized = values.map((value) => value?.trim()).filter(Boolean) as string[];
  return normalized.length > 0 ? normalized.join(" ") : undefined;
}

export function FieldHelp({
  label,
  helpText,
  hint,
  required = false,
  inputId,
  className,
  error,
  children,
}: FieldHelpProps) {
  const autoId = useId();
  const baseId = inputId ?? `field-${autoId}`;
  const helpId = `${baseId}-help`;
  const hintId = hint ? `${baseId}-hint` : undefined;
  const errorId = error ? `${baseId}-error` : undefined;

  const labelNode = inputId ? (
    <label htmlFor={inputId} className="flex items-center gap-1">
      <span>{label}</span>
      {required && (
        <>
          <span aria-hidden="true" className="text-destructive">
            *
          </span>
          <span className="sr-only">(required)</span>
        </>
      )}
    </label>
  ) : (
    <p className="flex items-center gap-1">
      <span>{label}</span>
      {required && (
        <>
          <span aria-hidden="true" className="text-destructive">
            *
          </span>
          <span className="sr-only">(required)</span>
        </>
      )}
    </p>
  );

  let control = children;
  if (inputId && isValidElement(children)) {
    const childElement = children as ReactElement<Record<string, unknown>>;
    const existingDescribedBy = typeof childElement.props["aria-describedby"] === "string"
      ? (childElement.props["aria-describedby"] as string)
      : undefined;
    const describedBy = joinDescribedBy([existingDescribedBy, helpId, hintId, errorId]);
    control = cloneElement(childElement, {
      id: inputId,
      required: required || Boolean(childElement.props.required),
      "aria-required": required || undefined,
      "aria-describedby": describedBy,
      "aria-invalid": error ? true : childElement.props["aria-invalid"],
    });
  }

  return (
    <div className={cn("space-y-1", className)}>
      <div className="flex items-center gap-1 text-xs text-muted-foreground">
        {labelNode}
        <div className="field-help-group relative inline-flex">
          <button
            type="button"
            className="rounded p-0.5 text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-cordum"
            aria-label={`Help for ${label}`}
            aria-describedby={helpId}
          >
            <CircleHelp className="h-3.5 w-3.5" />
          </button>
          <div id={helpId} role="tooltip" className="field-help-tooltip">
            {helpText}
          </div>
        </div>
      </div>
      {hint && (
        <p id={hintId} className="text-[11px] text-muted-foreground/90">
          {hint}
        </p>
      )}
      {control}
      {error && (
        <p id={errorId} className="text-[11px] text-destructive" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
