import { useState, useRef, useCallback, useId, useEffect, type KeyboardEvent } from "react";
import { cn } from "@/lib/utils";

export interface ComboboxSuggestion {
  value: string;
  label: string;
  description?: string;
}

interface ComboboxInputProps {
  suggestions: ComboboxSuggestion[];
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
  "aria-label"?: string;
}

export function ComboboxInput({
  suggestions,
  value,
  onChange,
  placeholder,
  className,
  "aria-label": ariaLabel,
}: ComboboxInputProps) {
  const [isOpen, setIsOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(-1);
  const inputRef = useRef<HTMLInputElement>(null);
  const listboxId = useId();

  const filtered = value.trim()
    ? suggestions.filter(
        (s) =>
          s.value.toLowerCase().includes(value.toLowerCase()) ||
          s.label.toLowerCase().includes(value.toLowerCase()),
      )
    : suggestions;

  const open = useCallback(() => {
    setIsOpen(true);
    setActiveIndex(-1);
  }, []);

  const close = useCallback(() => {
    setIsOpen(false);
    setActiveIndex(-1);
  }, []);

  const select = useCallback(
    (suggestion: ComboboxSuggestion) => {
      onChange(suggestion.value);
      close();
    },
    [onChange, close],
  );

  // Close on outside click
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (inputRef.current && !inputRef.current.parentElement?.contains(e.target as Node)) {
        close();
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [close]);

  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLInputElement>) => {
      if (!isOpen && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
        e.preventDefault();
        open();
        return;
      }
      if (!isOpen) return;

      switch (e.key) {
        case "ArrowDown":
          e.preventDefault();
          setActiveIndex((prev) => (prev < filtered.length - 1 ? prev + 1 : 0));
          break;
        case "ArrowUp":
          e.preventDefault();
          setActiveIndex((prev) => (prev > 0 ? prev - 1 : filtered.length - 1));
          break;
        case "Enter":
          e.preventDefault();
          if (activeIndex >= 0 && activeIndex < filtered.length) {
            select(filtered[activeIndex]);
          }
          break;
        case "Escape":
          e.preventDefault();
          close();
          break;
      }
    },
    [isOpen, filtered, activeIndex, open, close, select],
  );

  const activeOptionId = activeIndex >= 0 ? `${listboxId}-option-${activeIndex}` : undefined;

  return (
    <div className="relative">
      <input
        ref={inputRef}
        type="text"
        role="combobox"
        aria-expanded={isOpen}
        aria-controls={listboxId}
        aria-activedescendant={activeOptionId}
        aria-autocomplete="list"
        aria-label={ariaLabel ?? placeholder}
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
          open();
        }}
        onFocus={open}
        onKeyDown={handleKeyDown}
        placeholder={placeholder}
        className={cn(
          "flex h-9 w-full rounded-2xl border border-border bg-surface-2/50 px-3 py-2 text-sm text-foreground",
          "placeholder:text-muted-foreground/60",
          "focus:outline-none focus:ring-2 focus:ring-cordum/30 focus:border-cordum/40",
          "transition-all duration-150",
          className,
        )}
      />
      {isOpen && (
        <ul
          id={listboxId}
          role="listbox"
          className="absolute z-50 mt-1 w-full max-h-48 overflow-y-auto rounded-xl border border-border bg-surface-1 shadow-soft"
        >
          {filtered.length === 0 ? (
            <li className="px-3 py-2 text-xs text-muted-foreground">No matches</li>
          ) : (
            filtered.map((s, i) => (
              <li
                key={s.value}
                id={`${listboxId}-option-${i}`}
                role="option"
                aria-selected={i === activeIndex}
                onMouseDown={(e) => {
                  e.preventDefault();
                  select(s);
                }}
                onMouseEnter={() => setActiveIndex(i)}
                className={cn(
                  "flex flex-col px-3 py-2 cursor-pointer text-sm transition-colors",
                  i === activeIndex
                    ? "bg-cordum/10 text-foreground"
                    : "text-foreground hover:bg-surface-2",
                )}
              >
                <span className="font-medium">{s.label}</span>
                {s.description && (
                  <span className="text-xs text-muted-foreground">{s.description}</span>
                )}
              </li>
            ))
          )}
        </ul>
      )}
    </div>
  );
}
