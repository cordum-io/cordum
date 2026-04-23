import { forwardRef, type TextareaHTMLAttributes } from "react";
import { cn } from "../../lib/utils";

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(
  ({ className, ...props }, ref) => {
    return (
      <textarea
        ref={ref}
        className={cn(
          "w-full rounded-md border border-border bg-input px-4 py-3 text-sm text-foreground shadow-sm transition-all duration-micro ease-[cubic-bezier(0.16,1,0.3,1)] placeholder:text-muted-foreground/60 hover:border-accent/40 hover:shadow-soft focus:outline-none focus:border-accent focus:ring-2 focus:ring-accent/30 aria-[invalid=true]:border-danger disabled:bg-surface-1 disabled:text-muted-foreground disabled:cursor-not-allowed resize-y",
          className
        )}
        {...props}
      />
    );
  }
);

Textarea.displayName = "Textarea";
