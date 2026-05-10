import { useCallback, useId, useRef, useState, type KeyboardEvent } from "react";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";

interface TokenInputProps {
  value: string[];
  onChange: (next: string[]) => void;
  placeholder?: string;
  ariaLabel?: string;
  inputId?: string;
  ariaDescribedBy?: string;
  ariaInvalid?: boolean;
  disabled?: boolean;
}

/**
 * Compact token / chip input for string-array fields. Tokens render as
 * removable chips; typing into the trailing input + Enter / comma adds
 * a token; Backspace on the empty input removes the last chip; clicking
 * the chip's X removes that one.
 *
 * Single-consumer component (RuleFormView). Promote to `components/ui/`
 * when a second consumer arrives + co-located test.
 */
export function TokenInput({
  value,
  onChange,
  placeholder,
  ariaLabel,
  inputId,
  ariaDescribedBy,
  ariaInvalid,
  disabled = false,
}: TokenInputProps) {
  const [draft, setDraft] = useState("");
  const fallbackId = useId();
  const id = inputId ?? `token-input-${fallbackId}`;
  const inputRef = useRef<HTMLInputElement>(null);

  const commit = useCallback(
    (raw: string) => {
      const trimmed = raw.trim();
      if (!trimmed) return;
      // Dedupe — a token already in the list is a no-op rather than a
      // visible duplicate, since users typing the same value twice is
      // overwhelmingly a typo.
      if (value.includes(trimmed)) {
        setDraft("");
        return;
      }
      onChange([...value, trimmed]);
      setDraft("");
    },
    [onChange, value],
  );

  const removeAt = useCallback(
    (index: number) => {
      const next = value.slice(0, index).concat(value.slice(index + 1));
      onChange(next);
    },
    [onChange, value],
  );

  const onKeyDown = useCallback(
    (event: KeyboardEvent<HTMLInputElement>) => {
      if (event.key === "Enter" || event.key === ",") {
        event.preventDefault();
        commit(draft);
        return;
      }
      if (event.key === "Backspace" && draft.length === 0 && value.length > 0) {
        event.preventDefault();
        removeAt(value.length - 1);
      }
    },
    [commit, draft, removeAt, value.length],
  );

  return (
    <div
      className={cn(
        "flex min-h-9 w-full flex-wrap items-center gap-1.5 rounded-2xl border border-border bg-surface-2/50 px-2 py-1.5 text-sm",
        "focus-within:ring-2 focus-within:ring-cordum/30 focus-within:border-cordum/40",
        ariaInvalid && "border-destructive/50",
        disabled && "opacity-50 cursor-not-allowed",
      )}
      onClick={() => inputRef.current?.focus()}
      role="presentation"
    >
      {value.map((token, index) => (
        <span
          key={`${token}-${index}`}
          className="inline-flex items-center gap-1 rounded-full border border-cordum/20 bg-cordum/10 px-2 py-0.5 text-xs font-mono text-cordum"
        >
          <span className="max-w-[12rem] truncate">{token}</span>
          <button
            type="button"
            aria-label={`Remove ${token}`}
            onClick={(event) => {
              event.stopPropagation();
              if (!disabled) removeAt(index);
            }}
            className="-mr-0.5 rounded-full p-0.5 text-cordum/70 transition-colors hover:bg-cordum/20 hover:text-cordum focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-cordum"
            disabled={disabled}
          >
            <X aria-hidden className="h-3 w-3" />
          </button>
        </span>
      ))}
      <input
        ref={inputRef}
        id={id}
        type="text"
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onKeyDown={onKeyDown}
        onBlur={() => commit(draft)}
        placeholder={value.length === 0 ? placeholder : undefined}
        aria-label={ariaLabel ?? placeholder}
        aria-describedby={ariaDescribedBy}
        aria-invalid={ariaInvalid || undefined}
        disabled={disabled}
        className="flex-1 min-w-[6rem] bg-transparent px-1 py-0.5 text-sm text-foreground placeholder:text-muted-foreground/60 focus:outline-none disabled:cursor-not-allowed"
      />
    </div>
  );
}
