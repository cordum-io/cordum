import React from "react";

export default function Card({
  title,
  children,
  right,
}: {
  title?: string;
  right?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="glass rounded-2xl border border-primary-border p-4 shadow-sm">
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

