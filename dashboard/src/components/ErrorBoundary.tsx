import { Component, type ReactNode, type ErrorInfo } from "react";
import { logger } from "../lib/logger";

interface Props {
  children: ReactNode;
  resetKey?: string;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false, error: null };

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    logger.error("error-boundary", "Uncaught render error", {
      error: error.message,
      stack: error.stack,
      componentStack: info.componentStack ?? undefined,
    });
  }

  componentDidUpdate(prevProps: Props) {
    if (
      this.state.hasError &&
      prevProps.resetKey !== this.props.resetKey
    ) {
      this.setState({ hasError: false, error: null });
    }
  }

  render() {
    if (this.state.hasError) {
      const { error } = this.state;

      return (
        <div className="flex min-h-[300px] flex-col items-center justify-center gap-4 px-4 text-center">
          {/* Inline AlertTriangle SVG */}
          <svg
            xmlns="http://www.w3.org/2000/svg"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            className="h-12 w-12 text-warning"
            aria-hidden="true"
          >
            <path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3" />
            <path d="M12 9v4" />
            <path d="M12 17h.01" />
          </svg>

          <p className="text-lg font-semibold text-ink">Something went wrong</p>

          {error?.message && (
            <p className="max-w-md text-sm text-muted">{error.message}</p>
          )}

          {/* Dev-only stack trace */}
          {import.meta.env.DEV && error?.stack && (
            <details className="w-full max-w-2xl text-left">
              <summary className="cursor-pointer text-xs font-medium text-muted hover:text-ink">
                Stack trace
              </summary>
              <pre className="mt-2 overflow-auto rounded-xl bg-surface2 p-4 font-mono text-xs text-muted">
                {error.stack}
              </pre>
            </details>
          )}

          <div className="flex items-center gap-3">
            <button
              type="button"
              className="rounded-lg bg-accent px-4 py-2 text-xs font-semibold text-white transition hover:opacity-90"
              onClick={() => this.setState({ hasError: false, error: null })}
            >
              Retry
            </button>
            <button
              type="button"
              className="rounded-lg border border-border px-4 py-2 text-xs font-semibold text-ink transition hover:bg-surface2"
              onClick={() => { window.location.href = "/"; }}
            >
              Go to Overview
            </button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}
