import React from "react";

export default function Panel({
  title,
  children,
  right,
}: {
  title?: string;
  right?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-2xl border border-primary-border bg-secondary-background/60 p-4 shadow-sm">
      {title ? (
        <header className="mb-3 flex items-center justify-between">
          <div className="text-sm font-semibold text-primary-text">{title}</div>
          <div>{right}</div>
        </header>
      ) : null}
      <div>{children}</div>
    </section>
  );
}