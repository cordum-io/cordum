export default function Loading({ label }: { label?: string }) {
  return (
    <div className="rounded-xl border border-primary-border bg-secondary-background/60 p-4 text-sm text-primary-text">
      {label || "Loading..."}
    </div>
  );
}

