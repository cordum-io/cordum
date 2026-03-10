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
}

export function ComboboxInput({ suggestions, value, onChange, placeholder, className }: ComboboxInputProps) {
  return (
    <input
      type="text"
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      className={className}
    />
  );
}
