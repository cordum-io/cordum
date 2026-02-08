import { Component, type ReactNode, type ErrorInfo } from "react";
import { logger } from "../lib/logger";

interface Props {
  children: ReactNode;
}

interface State {
  hasError: boolean;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false };

  static getDerivedStateFromError(): State {
    return { hasError: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    logger.error("error-boundary", "Uncaught render error", {
      error: error.message,
      stack: error.stack,
      componentStack: info.componentStack ?? undefined,
    });
  }

  render() {
    if (this.state.hasError) {
      return (
        <div className="flex min-h-[300px] flex-col items-center justify-center gap-4 text-center">
          <p className="text-sm font-semibold text-ink">
            Something went wrong.
          </p>
          <button
            type="button"
            className="rounded-lg bg-accent px-4 py-2 text-xs font-semibold text-white transition hover:opacity-90"
            onClick={() => this.setState({ hasError: false })}
          >
            Retry
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
