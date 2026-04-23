import { type KeyboardEvent, useState, useRef, useCallback, useEffect, useId } from "react";
import { Input } from "./Input";
import { cn } from "../../lib/utils";

export interface ComboboxSuggestion {
  value: string;
  label: string;
  description?: string;
}

export interface ComboboxInputProps {
  value: string;
  onChange: (val: string) => void;
  suggestions: ComboboxSuggestion[];
  placeholder?: string;
  className?: string;
}

export function ComboboxInput({
  value,
  onChange,
  suggestions,
  placeholder,
  className,
}: ComboboxInputProps) {
  const [open, setOpen] = useState(false);
  const [activeIdx, setActiveIdx] = useState(-1);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const listboxId = useId();

  // Filter suggestions by fuzzy match on value or label
  const filtered = suggestions.filter((s) => {
    const q = value.toLowerCase();
    return s.value.toLowerCase().includes(q) || s.label.toLowerCase().includes(q);
  });

  // Close on click outside
  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (wrapperRef.current && !wrapperRef.current.contains(e.target as globalThis.Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  // Reset active index when filtered list changes
  useEffect(() => {
    setActiveIdx(-1);
  }, [value]);

  const handleSelect = useCallback(
    (val: string) => {
      onChange(val);
      setOpen(false);
    },
    [onChange],
  );

  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLInputElement>) => {
      if (!open || filtered.length === 0) return;

      if (e.key === "ArrowDown") {
        e.preventDefault();
        setActiveIdx((prev) => (prev < filtered.length - 1 ? prev + 1 : 0));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setActiveIdx((prev) => (prev > 0 ? prev - 1 : filtered.length - 1));
      } else if (e.key === "Enter" && activeIdx >= 0) {
        e.preventDefault();
        handleSelect(filtered[activeIdx].value);
      } else if (e.key === "Escape") {
        setOpen(false);
      }
    },
    [open, filtered, activeIdx, handleSelect],
  );

  return (
    <div ref={wrapperRef} className="relative">
      <Input
        type="text"
        value={value}
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={open && filtered.length > 0}
        aria-controls={listboxId}
        onChange={(e) => {
          onChange(e.target.value);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={handleKeyDown}
        placeholder={placeholder}
        className={cn("px-4 py-2.5", className)}
      />
      {open && filtered.length > 0 && (
        <ul
          id={listboxId}
          role="listbox"
          className="absolute left-0 right-0 z-50 mt-1 max-h-48 overflow-y-auto rounded-lg border border-border bg-surface-1 shadow-soft"
        >
          {filtered.map((s, i) => (
            <li
              key={s.value}
              role="option"
              aria-selected={i === activeIdx}
            >
              <button
                type="button"
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => handleSelect(s.value)}
                className={cn(
                  "flex w-full flex-col px-3 py-2 text-left text-sm transition-colors duration-micro",
                  i === activeIdx
                    ? "bg-status-info-bg text-foreground"
                    : "text-foreground hover:bg-surface-2/70",
                )}
              >
                <span className="font-medium">{s.label}</span>
                {s.description && (
                  <span className="text-[10px] text-muted-foreground">{s.description}</span>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
