import type { ReactNode } from "react";
import { FieldHelp } from "@/components/ui/FieldHelp";

interface PolicyFieldProps {
  label: string;
  inputId?: string;
  required?: boolean;
  helpText: string;
  hint?: string;
  error?: string | null;
  className?: string;
  children: ReactNode;
}

export function PolicyField({
  label,
  inputId,
  required = false,
  helpText,
  hint,
  error,
  className,
  children,
}: PolicyFieldProps) {
  return (
    <FieldHelp
      label={label}
      inputId={inputId}
      required={required}
      helpText={helpText}
      hint={hint}
      error={error}
      className={className}
    >
      {children}
    </FieldHelp>
  );
}
