import { useState, useRef, useCallback, useEffect } from "react";
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
    (e: React.KeyboardEvent) => {
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
      <input
        type="text"
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={handleKeyDown}
        placeholder={placeholder}
        className={cn(
          "w-full rounded-2xl border border-border bg-white/70 px-4 py-2.5 text-sm text-ink shadow-sm transition-all duration-200 ease-[cubic-bezier(0.16,1,0.3,1)] placeholder:text-muted/60 hover:border-[color:rgba(15,127,122,0.4)] hover:shadow-soft focus:outline-none focus:border-accent focus:ring-2 focus:ring-[color:var(--ring)]",
          className,
        )}
      />
      {open && filtered.length > 0 && (
        <ul className="absolute left-0 right-0 z-50 mt-1 max-h-48 overflow-y-auto rounded-xl border border-border bg-white shadow-lg">
          {filtered.map((s, i) => (
            <li
              key={s.value}
              onMouseDown={() => handleSelect(s.value)}
              className={cn(
                "flex flex-col cursor-pointer px-3 py-2 text-sm",
                i === activeIdx ? "bg-accent/10 text-ink" : "text-ink hover:bg-surface2",
              )}
            >
              <span className="font-medium">{s.label}</span>
              {s.description && (
                <span className="text-[10px] text-muted">{s.description}</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
