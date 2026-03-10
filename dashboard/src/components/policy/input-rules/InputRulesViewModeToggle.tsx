interface InputRulesViewModeToggleProps {
  mode: "visual" | "split" | "yaml";
  onChange: (mode: "visual" | "split" | "yaml") => void;
}

export function InputRulesViewModeToggle({
  mode,
  onChange,
}: InputRulesViewModeToggleProps) {
  return (
    <div
      className="flex items-center gap-1 rounded-md border border-border bg-surface-1 p-1"
      role="group"
      aria-label="Input rules page view mode"
    >
      {(["visual", "split", "yaml"] as const).map((candidate) => (
        <button
          key={candidate}
          type="button"
          className={`rounded px-2 py-1 text-[10px] font-mono uppercase tracking-wide ${
            mode === candidate
              ? "bg-cordum/15 text-cordum"
              : "text-muted-foreground hover:text-foreground"
          }`}
          aria-pressed={mode === candidate}
          onClick={() => onChange(candidate)}
        >
          {candidate}
        </button>
      ))}
    </div>
  );
}
