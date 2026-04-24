/**
 * Page-level skeleton loading states for each major page type.
 * Used inside Suspense boundaries and while data is loading.
 */

export function DashboardSkeleton() {
  return (
    <div className="space-y-6 animate-in fade-in duration-300">
      {/* KPI Row */}
      <div className="grid grid-cols-4 gap-4">
        {[...Array(4)].map((_, i) => (
          <div key={i} className="instrument-card space-y-3">
            <div className="skeleton h-3 w-20" />
            <div className="skeleton h-8 w-24" />
            <div className="skeleton h-2.5 w-16" />
          </div>
        ))}
      </div>
      {/* Chart */}
      <div className="instrument-card">
        <div className="skeleton h-3 w-32 mb-4" />
        <div className="skeleton h-48 w-full" />
      </div>
      {/* Table */}
      <div className="instrument-card space-y-3">
        <div className="skeleton h-3 w-28 mb-4" />
        {[...Array(5)].map((_, i) => (
          <div key={i} className="flex gap-4">
            <div className="skeleton h-4 w-24" />
            <div className="skeleton h-4 flex-1" />
            <div className="skeleton h-4 w-16" />
            <div className="skeleton h-4 w-20" />
          </div>
        ))}
      </div>
    </div>
  );
}

export function TableSkeleton({ rows = 8 }: { rows?: number }) {
  return (
    <div className="space-y-6 animate-in fade-in duration-300">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="space-y-2">
          <div className="skeleton h-3 w-16" />
          <div className="skeleton h-7 w-40" />
        </div>
        <div className="skeleton h-9 w-28 rounded-full" />
      </div>
      {/* Filters */}
      <div className="flex gap-3">
        <div className="skeleton h-9 w-56 rounded-full" />
        <div className="skeleton h-9 w-28 rounded-full" />
        <div className="skeleton h-9 w-28 rounded-full" />
      </div>
      {/* Table */}
      <div className="instrument-card overflow-hidden">
        <div className="bg-surface-0 px-4 py-3 flex gap-4 border-b border-border">
          {[...Array(5)].map((_, i) => (
            <div key={i} className="skeleton h-3 flex-1" />
          ))}
        </div>
        {[...Array(rows)].map((_, i) => (
          <div key={i} className="px-4 py-3.5 flex gap-4 border-b border-border/50">
            {[...Array(5)].map((_, j) => (
              <div key={j} className="skeleton h-4 flex-1" />
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}

export function DetailSkeleton() {
  return (
    <div className="space-y-6 animate-in fade-in duration-300">
      {/* Back + Title */}
      <div className="space-y-3">
        <div className="skeleton h-3 w-16" />
        <div className="skeleton h-7 w-64" />
        <div className="skeleton h-4 w-40" />
      </div>
      {/* Tabs */}
      <div className="flex gap-2">
        {[...Array(4)].map((_, i) => (
          <div key={i} className="skeleton h-8 w-20 rounded-full" />
        ))}
      </div>
      {/* Content */}
      <div className="grid grid-cols-2 gap-4">
        <div className="instrument-card space-y-3">
          {[...Array(6)].map((_, i) => (
            <div key={i} className="flex justify-between">
              <div className="skeleton h-3 w-20" />
              <div className="skeleton h-3 w-32" />
            </div>
          ))}
        </div>
        <div className="instrument-card">
          <div className="skeleton h-48 w-full" />
        </div>
      </div>
    </div>
  );
}

export function CardGridSkeleton({ count = 6 }: { count?: number }) {
  return (
    <div className="space-y-6 animate-in fade-in duration-300">
      <div className="flex items-center justify-between">
        <div className="space-y-2">
          <div className="skeleton h-3 w-16" />
          <div className="skeleton h-7 w-40" />
        </div>
        <div className="skeleton h-9 w-28 rounded-full" />
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {[...Array(count)].map((_, i) => (
          <div key={i} className="instrument-card space-y-3">
            <div className="flex items-center gap-3">
              <div className="skeleton w-10 h-10 rounded-xl" />
              <div className="space-y-1.5 flex-1">
                <div className="skeleton h-4 w-28" />
                <div className="skeleton h-3 w-20" />
              </div>
            </div>
            <div className="skeleton h-3 w-full" />
            <div className="skeleton h-3 w-3/4" />
          </div>
        ))}
      </div>
    </div>
  );
}

export function FormSkeleton() {
  return (
    <div className="space-y-6 animate-in fade-in duration-300 max-w-2xl">
      <div className="space-y-2">
        <div className="skeleton h-3 w-16" />
        <div className="skeleton h-7 w-48" />
      </div>
      {[...Array(5)].map((_, i) => (
        <div key={i} className="space-y-2">
          <div className="skeleton h-3 w-24" />
          <div className="skeleton h-10 w-full rounded-full" />
        </div>
      ))}
      <div className="flex gap-3 pt-4">
        <div className="skeleton h-9 w-20 rounded-full" />
        <div className="skeleton h-9 w-24 rounded-full" />
      </div>
    </div>
  );
}
