export interface ChartTooltipPayloadEntry {
  name: string;
  value: number;
  color?: string;
  dataKey?: string;
}

export interface ChartTooltipProps {
  active?: boolean;
  payload?: ChartTooltipPayloadEntry[];
  label?: string;
}

export function ChartTooltip({ active, payload, label }: ChartTooltipProps) {
  if (!active || !payload?.length) return null;
  return (
    <div className="rounded-2xl border border-border bg-[color:var(--surface-glass)] p-3 shadow-soft backdrop-blur-md">
      <p className="font-mono text-xs text-muted-foreground mb-1">{label}</p>
      {payload.map((entry, index) => (
        <div key={entry.name ?? index} className="flex items-center gap-2 text-xs">
          <span className="w-2 h-2 rounded-full" style={{ backgroundColor: entry.color }} />
          <span className="text-muted-foreground">{entry.name}:</span>
          <span className="font-mono text-foreground font-medium">{entry.value}</span>
        </div>
      ))}
    </div>
  );
}

export function ChartTooltipCompact({ active, payload, label }: ChartTooltipProps) {
  if (!active || !payload?.length) return null;
  return (
    <div className="rounded-2xl border border-border bg-[color:var(--surface-glass)] p-2 shadow-soft backdrop-blur-md">
      <p className="font-mono text-[10px] text-muted-foreground mb-1">{label}</p>
      {payload.map((entry, index) => (
        <div key={entry.name ?? index} className="flex items-center gap-2 text-[10px]">
          <span className="w-2 h-2 rounded-full" style={{ backgroundColor: entry.color }} />
          <span className="text-muted-foreground">{entry.name}:</span>
          <span className="font-mono text-foreground">{entry.value}</span>
        </div>
      ))}
    </div>
  );
}
