import { useState, useRef, useCallback, type KeyboardEvent } from "react";
import { X } from "lucide-react";
import { Badge } from "./Badge";
import { cn } from "../../lib/utils";

interface TagInputProps {
  value: string[];
  onChange: (tags: string[]) => void;
  suggestions?: string[];
  maxTags?: number;
  placeholder?: string;
  className?: string;
}

export function TagInput({
  value,
  onChange,
  suggestions,
  maxTags,
  placeholder = "Add tag…",
  className,
}: TagInputProps) {
  const [input, setInput] = useState("");
  const [showSuggestions, setShowSuggestions] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const addTag = useCallback(
    (tag: string) => {
      const trimmed = tag.trim();
      if (!trimmed) return;
      if (value.includes(trimmed)) return;
      if (maxTags && value.length >= maxTags) return;
      onChange([...value, trimmed]);
      setInput("");
      setShowSuggestions(false);
    },
    [value, onChange, maxTags],
  );

  const removeTag = useCallback(
    (idx: number) => {
      onChange(value.filter((_, i) => i !== idx));
    },
    [value, onChange],
  );

  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLInputElement>) => {
      if (e.key === "Enter" || e.key === ",") {
        e.preventDefault();
        addTag(input);
      } else if (e.key === "Backspace" && input === "" && value.length > 0) {
        removeTag(value.length - 1);
      }
    },
    [input, value, addTag, removeTag],
  );

  const filtered = suggestions?.filter(
    (s) => s.toLowerCase().includes(input.toLowerCase()) && !value.includes(s),
  );

  return (
    <div className={cn("relative", className)}>
      <div
        className="flex min-h-[42px] flex-wrap items-center gap-1.5 rounded-2xl border border-border bg-white/70 px-3 py-2 shadow-sm transition-all duration-200 focus-within:border-accent focus-within:ring-2 focus-within:ring-[color:var(--ring)]"
        onClick={() => inputRef.current?.focus()}
      >
        {value.map((tag, idx) => (
          <Badge key={tag} variant="default" className="gap-1 py-0.5 pl-2.5 pr-1.5">
            {tag}
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                removeTag(idx);
              }}
              className="rounded-full p-0.5 transition hover:bg-ink/10"
            >
              <X className="h-3 w-3" />
            </button>
          </Badge>
        ))}
        <input
          ref={inputRef}
          type="text"
          value={input}
          onChange={(e) => {
            setInput(e.target.value);
            setShowSuggestions(true);
          }}
          onKeyDown={handleKeyDown}
          onFocus={() => setShowSuggestions(true)}
          onBlur={() => setTimeout(() => setShowSuggestions(false), 150)}
          placeholder={value.length === 0 ? placeholder : ""}
          className="min-w-[120px] flex-1 border-none bg-transparent text-sm text-ink outline-none placeholder:text-muted/60"
        />
      </div>

      {/* Suggestions dropdown */}
      {showSuggestions && input && filtered && filtered.length > 0 && (
        <ul className="absolute z-20 mt-1 max-h-40 w-full overflow-y-auto rounded-xl border border-border bg-surface shadow-lift">
          {filtered.map((s) => (
            <li key={s}>
              <button
                type="button"
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => addTag(s)}
                className="w-full px-3 py-2 text-left text-sm text-ink transition hover:bg-surface2/50"
              >
                {s}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
