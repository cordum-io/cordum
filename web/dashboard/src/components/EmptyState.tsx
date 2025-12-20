export default function EmptyState({
  title,
  description,
}: {
  title: string;
  description?: string;
}) {
  return (
    <div className="rounded-xl border border-primary-border bg-secondary-background/60 p-6 text-sm text-primary-text">
      <div className="text-base font-semibold text-primary-text">{title}</div>
      {description ? <div className="mt-1 text-tertiary-text">{description}</div> : null}
    </div>
  );
}

