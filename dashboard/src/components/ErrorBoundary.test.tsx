import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, fireEvent, cleanup } from "@testing-library/react";
import { ErrorBoundary } from "./ErrorBoundary";

function Boom({ message = "boom" }: { message?: string }): never {
  throw new Error(message);
}

function Healthy() {
  return <span data-testid="healthy">healthy</span>;
}

describe("ErrorBoundary primitive", () => {
  let errorSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
  });

  afterEach(() => {
    errorSpy.mockRestore();
    cleanup();
  });

  it("renders children unchanged when nothing throws", () => {
    const { getByTestId } = render(
      <ErrorBoundary>
        <Healthy />
      </ErrorBoundary>,
    );
    expect(getByTestId("healthy").textContent).toBe("healthy");
  });

  it("renders the default fallback UI when a child throws and no fallback prop is supplied", () => {
    const { getByText } = render(
      <ErrorBoundary>
        <Boom message="default-fallback" />
      </ErrorBoundary>,
    );
    expect(getByText("Something went wrong")).toBeTruthy();
    expect(getByText("default-fallback")).toBeTruthy();
    expect(getByText("Retry")).toBeTruthy();
  });

  it("renders the custom fallback when fallback prop is supplied, passing error + reset", () => {
    const fallback = vi.fn(({ error, reset }) => (
      <div data-testid="custom-fallback">
        <span>err:{error.message}</span>
        <button type="button" onClick={reset}>
          custom-reset
        </button>
      </div>
    ));
    const { getByTestId, getByText } = render(
      <ErrorBoundary fallback={fallback}>
        <Boom message="custom-fallback" />
      </ErrorBoundary>,
    );
    expect(getByTestId("custom-fallback")).toBeTruthy();
    expect(getByText("err:custom-fallback")).toBeTruthy();
    expect(fallback).toHaveBeenCalledWith(
      expect.objectContaining({
        error: expect.any(Error),
        reset: expect.any(Function),
      }),
    );
  });

  it("custom fallback's reset clears the boundary so children re-render", () => {
    function ToggleableChild({ shouldThrow }: { shouldThrow: boolean }) {
      if (shouldThrow) {
        throw new Error("first-render");
      }
      return <span data-testid="recovered">recovered</span>;
    }

    const shouldFlag = { current: true };
    function Harness() {
      return (
        <ErrorBoundary
          fallback={({ reset }) => (
            <button type="button" onClick={reset}>
              custom-reset
            </button>
          )}
        >
          <ToggleableChild shouldThrow={shouldFlag.current} />
        </ErrorBoundary>
      );
    }

    const { rerender, getByText, getByTestId } = render(<Harness />);
    expect(getByText("custom-reset")).toBeTruthy();
    // Simulate the underlying problem resolving (network blip, etc.) BEFORE
    // user clicks Retry: rerender the parent so the boundary's `children`
    // prop carries shouldThrow=false. The boundary still shows the fallback
    // until reset clears hasError.
    shouldFlag.current = false;
    rerender(<Harness />);
    expect(getByText("custom-reset")).toBeTruthy();
    fireEvent.click(getByText("custom-reset"));
    expect(getByTestId("recovered").textContent).toBe("recovered");
  });

  it("resetKey change auto-clears the boundary on the next render", () => {
    function ToggleableChild({ shouldThrow }: { shouldThrow: boolean }) {
      if (shouldThrow) {
        throw new Error("transient");
      }
      return <span data-testid="recovered">recovered</span>;
    }
    const { rerender, getByText, getByTestId } = render(
      <ErrorBoundary resetKey="/a">
        <ToggleableChild shouldThrow={true} />
      </ErrorBoundary>,
    );
    expect(getByText("Something went wrong")).toBeTruthy();
    rerender(
      <ErrorBoundary resetKey="/b">
        <ToggleableChild shouldThrow={false} />
      </ErrorBoundary>,
    );
    expect(getByTestId("recovered").textContent).toBe("recovered");
  });
});
