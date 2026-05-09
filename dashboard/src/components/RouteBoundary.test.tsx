import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, fireEvent, cleanup } from "@testing-library/react";
import { MemoryRouter, Routes, Route, Link } from "react-router-dom";
import { RouteBoundary } from "./RouteBoundary";

function Boom({ message = "boom" }: { message?: string }): never {
  throw new Error(message);
}

function Healthy({ label }: { label: string }) {
  return <div data-testid={`page-${label}`}>page {label}</div>;
}

function NavLink({ to, children }: { to: string; children: string }) {
  return (
    <Link to={to} data-testid={`link-${children.toLowerCase()}`}>
      {children}
    </Link>
  );
}

describe("RouteBoundary integration", () => {
  let errorSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
  });

  afterEach(() => {
    errorSpy.mockRestore();
    cleanup();
  });

  it("renders RouteErrorFallback (with route name) when the wrapped page throws", () => {
    const { getByText, queryByText } = render(
      <MemoryRouter initialEntries={["/jobs"]}>
        <Routes>
          <Route
            path="/jobs"
            element={
              <RouteBoundary name="Jobs">
                <Boom message="JobsPage failed" />
              </RouteBoundary>
            }
          />
          <Route
            path="/agents"
            element={
              <RouteBoundary name="Agents">
                <Healthy label="agents" />
              </RouteBoundary>
            }
          />
        </Routes>
      </MemoryRouter>,
    );
    expect(getByText("Couldn't load Jobs")).toBeTruthy();
    expect(getByText("JobsPage failed")).toBeTruthy();
    // The route name is scoped to Jobs — Agents fallback text never appears.
    expect(queryByText("Couldn't load Agents")).toBeNull();
  });

  it("navigating away from the errored route renders the next page (boundary auto-resets via resetKey)", () => {
    const { getByText, getByTestId, queryByText } = render(
      <MemoryRouter initialEntries={["/jobs"]}>
        <nav>
          <NavLink to="/jobs">Jobs</NavLink>
          <NavLink to="/agents">Agents</NavLink>
        </nav>
        <Routes>
          <Route
            path="/jobs"
            element={
              <RouteBoundary name="Jobs">
                <Boom message="JobsPage failed" />
              </RouteBoundary>
            }
          />
          <Route
            path="/agents"
            element={
              <RouteBoundary name="Agents">
                <Healthy label="agents" />
              </RouteBoundary>
            }
          />
        </Routes>
      </MemoryRouter>,
    );
    expect(getByText("Couldn't load Jobs")).toBeTruthy();
    fireEvent.click(getByTestId("link-agents"));
    expect(getByTestId("page-agents").textContent).toBe("page agents");
    // The /jobs error fallback is gone now that we've navigated.
    expect(queryByText("Couldn't load Jobs")).toBeNull();
  });

  it("Retry on the fallback re-mounts the route subtree (so a re-rendered child can recover)", () => {
    let throwOnNextRender = true;
    function Recoverable() {
      if (throwOnNextRender) {
        throw new Error("transient");
      }
      return <div data-testid="page-jobs-recovered">recovered</div>;
    }
    const { getByText, getByTestId } = render(
      <MemoryRouter initialEntries={["/jobs"]}>
        <Routes>
          <Route
            path="/jobs"
            element={
              <RouteBoundary name="Jobs">
                <Recoverable />
              </RouteBoundary>
            }
          />
        </Routes>
      </MemoryRouter>,
    );
    expect(getByText("Couldn't load Jobs")).toBeTruthy();
    throwOnNextRender = false;
    fireEvent.click(getByText("Retry"));
    expect(getByTestId("page-jobs-recovered").textContent).toBe("recovered");
  });
});
