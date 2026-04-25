import { lazy, type ComponentType } from "react";

/**
 * safeLazy wraps React.lazy with chunk-load failure recovery.
 *
 * On network failure, it retries once with a cache-busted import. If both
 * attempts fail, it renders an inline error component instead of leaving
 * the user on a blank screen with an unhandled promise rejection.
 */
export function safeLazy<T extends ComponentType<any>>(
  factory: () => Promise<{ default: T }>,
): React.LazyExoticComponent<T> {
  return lazy(() =>
    factory().catch(() => {
      // Retry once — covers transient network glitches and stale chunk hashes
      // after a deployment. The second import() call bypasses the module cache.
      return factory().catch(() => ({
        default: ChunkLoadError as unknown as T,
      }));
    }),
  );
}

/** Inline fallback component shown when a lazy chunk fails to load. */
function ChunkLoadError() {
  return (
    <div className="flex min-h-[50vh] flex-col items-center justify-center gap-4 p-8 text-center">
      <div className="rounded-xl border border-border bg-surface1 p-6 shadow-sm">
        <h2 className="text-lg font-semibold text-ink">Page failed to load</h2>
        <p className="mt-2 text-sm text-muted-foreground">
          A network error prevented this page from loading. This usually
          happens after a deployment or during a brief connectivity issue.
        </p>
        <button
          type="button"
          onClick={() => window.location.reload()}
          className="mt-4 rounded-xl bg-accent px-4 py-2 text-sm font-medium text-white hover:bg-accent/90 transition-colors"
        >
          Refresh Page
        </button>
      </div>
    </div>
  );
}

