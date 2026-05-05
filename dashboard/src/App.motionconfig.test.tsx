import React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@/test-utils/render";

vi.mock("sonner", () => ({
  Toaster: () => null,
}));

vi.mock("./state/ui", () => ({
  useUiStore: (selector: (state: { resolvedTheme: string }) => unknown) =>
    selector({ resolvedTheme: "dark" }),
}));

vi.mock("./hooks/useAuth", () => ({
  useRequireAuth: () => true,
}));

vi.mock("./hooks/useEventStream", () => ({
  useEventStream: () => undefined,
}));

vi.mock("./components/ToastBridge", () => ({
  ToastBridge: () => null,
}));

vi.mock("./components/ErrorBoundary", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

vi.mock("./components/layout/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="app-shell">{children}</div>
  ),
}));

vi.mock("./components/layout/LoadingScreen", () => ({
  LoadingScreen: () => <div data-testid="loading-screen">loading</div>,
}));

vi.mock("framer-motion", async () => {
  const React = await import("react");

  type ReducedMotionSetting = "always" | "never" | "user" | undefined;

  const ReducedMotionContext = React.createContext<ReducedMotionSetting>(undefined);

  function prefersReducedMotion(setting: ReducedMotionSetting) {
    if (setting === "always") return true;
    if (setting !== "user") return false;
    return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  }

  const MotionConfig = ({
    children,
    reducedMotion,
  }: {
    children: React.ReactNode;
    reducedMotion?: ReducedMotionSetting;
  }) => (
    <ReducedMotionContext.Provider value={reducedMotion}>
      {children}
    </ReducedMotionContext.Provider>
  );

  const MotionDiv = ({
    animate,
    children,
    initial,
    style,
    transition,
    ...rest
  }: {
    animate?: { opacity?: number; x?: number };
    children?: React.ReactNode;
    initial?: false | { opacity?: number; x?: number };
    style?: React.CSSProperties;
    transition?: { duration?: number };
    [key: string]: unknown;
  }) => {
    const reducedMotion = React.useContext(ReducedMotionContext);
    const resolvedStyle: React.CSSProperties = { ...(style ?? {}) };

    if (prefersReducedMotion(reducedMotion)) {
      resolvedStyle.opacity = animate?.opacity ?? 1;
      resolvedStyle.transform = "none";
      resolvedStyle.transitionDuration = "0s";
    } else if (initial && typeof initial === "object") {
      if (typeof initial.opacity === "number") {
        resolvedStyle.opacity = initial.opacity;
      }
      if (typeof initial.x === "number") {
        resolvedStyle.transform = `translateX(${initial.x}px)`;
      }
      if (transition?.duration != null) {
        resolvedStyle.transitionDuration = `${transition.duration}s`;
      }
    }

    return (
      <div style={resolvedStyle} {...rest}>
        {children}
      </div>
    );
  };

  return {
    MotionConfig,
    motion: {
      div: MotionDiv,
    },
  };
});

vi.mock("./pages/HomePage", async () => {
  const { motion } = await import("framer-motion");

  return {
    default: function MockHomePage() {
      return (
        <motion.div
          data-testid="home-motion-root"
          initial={{ opacity: 0, x: 24 }}
          animate={{ opacity: 1, x: 0 }}
          transition={{ duration: 0.4 }}
        >
          Home
        </motion.div>
      );
    },
  };
});

import App from "./App";

describe("App MotionConfig wiring", () => {
  const originalMatchMedia = window.matchMedia;
  let prefersReduce = false;

  beforeEach(() => {
    prefersReduce = false;
    window.history.pushState({}, "", "/");
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      writable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: query === "(prefers-reduced-motion: reduce)" ? prefersReduce : false,
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
  });

  afterEach(() => {
    cleanup();
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      writable: true,
      value: originalMatchMedia,
    });
  });

  it("honors prefers-reduced-motion when user prefers reduce", async () => {
    prefersReduce = true;

    render(<App />);

    const root = await screen.findByTestId("home-motion-root");
    const style = root.getAttribute("style") ?? "";

    expect(style).toContain("opacity: 1");
    expect(style).toContain("transform: none");
    expect(style).toContain("transition-duration: 0s");
  });

  it("leaves motion enabled when user has no reduce preference", async () => {
    render(<App />);

    const root = await screen.findByTestId("home-motion-root");
    const style = root.getAttribute("style") ?? "";

    expect(style).toContain("opacity: 0");
    expect(style).toContain("transform: translateX(24px)");
    expect(style).toContain("transition-duration: 0.4s");
    expect(style).not.toContain("transition-duration: 0s");
  });
});
