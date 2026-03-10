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
        <div className="flex min-h-[300px] items-center justify-center px-4 py-6">
          <div className="w-full max-w-2xl rounded-3xl border border-border bg-[color:var(--surface-glass)] p-6 text-center shadow-soft backdrop-blur-xl">
            <svg
              xmlns="http://www.w3.org/2000/svg"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              className="mx-auto h-12 w-12 text-[var(--color-warning)]"
              aria-hidden="true"
            >
              <path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3" />
              <path d="M12 9v4" />
              <path d="M12 17h.01" />
            </svg>

            <p className="mt-3 text-lg font-semibold text-foreground">Something went wrong</p>

            {error?.message && (
              <p className="mx-auto mt-2 max-w-md text-sm text-muted-foreground">{error.message}</p>
            )}

            {import.meta.env.DEV && error?.stack && (
              <details className="mx-auto mt-4 w-full max-w-2xl text-left">
                <summary className="cursor-pointer text-xs font-medium text-muted-foreground hover:text-foreground">
                  Stack trace
                </summary>
                <pre className="mt-2 overflow-auto rounded-2xl bg-surface-2 p-4 font-mono text-xs text-muted-foreground">
                  {error.stack}
                </pre>
              </details>
            )}

            <div className="mt-5 flex items-center justify-center gap-3">
              <button
                type="button"
                className="rounded-full bg-primary px-4 py-2 text-xs font-semibold text-primary-foreground shadow-glow transition hover:bg-primary/90"
                onClick={() => this.setState({ hasError: false, error: null })}
              >
                Retry
              </button>
              <button
                type="button"
                className="rounded-full border border-border px-4 py-2 text-xs font-semibold text-foreground transition hover:bg-secondary"
                onClick={() => { window.location.href = "/"; }}
              >
                Go to Overview
              </button>
            </div>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}
