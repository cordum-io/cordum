import { type ReactNode } from "react";
import { useLocation } from "react-router-dom";
import { ErrorBoundary } from "./ErrorBoundary";
import { RouteErrorFallback } from "./RouteErrorFallback";

export interface RouteBoundaryProps {
  name: string;
  children: ReactNode;
}

/**
 * RouteBoundary wraps a single Route's element with an ErrorBoundary that
 * renders a route-scoped RouteErrorFallback (with route name + Retry +
 * Report bug link). The location-aware resetKey clears the boundary when
 * the user navigates away (componentDidUpdate inside ErrorBoundary).
 *
 * The outermost ErrorBoundaryWrapper in App.tsx is retained as the safety
 * net for AppShell-level / Suspense-fallback render errors that bypass any
 * specific Route.
 */
export function RouteBoundary({ name, children }: RouteBoundaryProps) {
  const location = useLocation();
  return (
    <ErrorBoundary
      resetKey={location.pathname}
      fallback={({ error, reset }) => (
        <RouteErrorFallback route={name} error={error} reset={reset} />
      )}
    >
      {children}
    </ErrorBoundary>
  );
}
