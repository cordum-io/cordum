import { describe, expect, it } from "vitest";
import {
  SEMANTIC_COLORS,
  semanticColor,
  gridProps,
  axisTickStyle,
  axisLineProps,
  tooltipStyle,
  barDefaults,
  areaDefaults,
  chartMotionProps,
} from "./chart-theme";

// ---------------------------------------------------------------------------
// Semantic colors
// ---------------------------------------------------------------------------

describe("SEMANTIC_COLORS", () => {
  it("maps allow to success token", () => {
    expect(SEMANTIC_COLORS.allow).toBe("var(--success)");
  });

  it("maps deny to danger token", () => {
    expect(SEMANTIC_COLORS.deny).toBe("var(--danger)");
  });

  it("maps require_approval to warning token", () => {
    expect(SEMANTIC_COLORS.require_approval).toBe("var(--warning)");
  });

  it("maps throttle to muted token", () => {
    expect(SEMANTIC_COLORS.throttle).toBe("var(--muted)");
  });

  it("maps running to accent token", () => {
    expect(SEMANTIC_COLORS.running).toBe("var(--accent)");
  });

  it("maps succeeded to success token", () => {
    expect(SEMANTIC_COLORS.succeeded).toBe("var(--success)");
  });

  it("maps failed to danger token", () => {
    expect(SEMANTIC_COLORS.failed).toBe("var(--danger)");
  });
});

describe("semanticColor", () => {
  it("returns known color for valid key", () => {
    expect(semanticColor("allow")).toBe("var(--success)");
    expect(semanticColor("deny")).toBe("var(--danger)");
  });

  it("returns muted fallback for unknown key", () => {
    expect(semanticColor("unknown_status")).toBe("var(--muted)");
    expect(semanticColor("")).toBe("var(--muted)");
  });
});

// ---------------------------------------------------------------------------
// Grid & axis
// ---------------------------------------------------------------------------

describe("gridProps", () => {
  it("uses dashed stroke pattern", () => {
    expect(gridProps.strokeDasharray).toBe("3 3");
  });

  it("uses tokenized border color", () => {
    expect(gridProps.stroke).toBe("var(--border)");
  });

  it("has reduced opacity", () => {
    expect(gridProps.strokeOpacity).toBeLessThan(1);
  });
});

describe("axisTickStyle", () => {
  it("uses mono font family", () => {
    expect(axisTickStyle.fontFamily).toContain("IBM Plex Mono");
  });

  it("uses small font size", () => {
    expect(axisTickStyle.fontSize).toBeLessThanOrEqual(12);
  });

  it("uses muted fill token", () => {
    expect(axisTickStyle.fill).toBe("var(--muted)");
  });
});

describe("axisLineProps", () => {
  it("hides axis line", () => {
    expect(axisLineProps.axisLine).toBe(false);
  });

  it("hides tick line", () => {
    expect(axisLineProps.tickLine).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// Tooltip
// ---------------------------------------------------------------------------

describe("tooltipStyle", () => {
  it("uses surface background token", () => {
    expect(tooltipStyle.backgroundColor).toBe("var(--surface)");
  });

  it("uses border token", () => {
    expect(tooltipStyle.border).toContain("var(--border)");
  });

  it("uses compact border radius", () => {
    expect(tooltipStyle.borderRadius).toBeLessThanOrEqual(16);
  });

  it("uses shadow token", () => {
    expect(tooltipStyle.boxShadow).toContain("var(--shadow)");
  });
});

// ---------------------------------------------------------------------------
// Bar & area defaults
// ---------------------------------------------------------------------------

describe("barDefaults", () => {
  it("has top-rounded corners", () => {
    const [tl, tr, br, bl] = barDefaults.radius;
    expect(tl).toBeGreaterThan(0);
    expect(tr).toBeGreaterThan(0);
    expect(br).toBe(0);
    expect(bl).toBe(0);
  });

  it("constrains max bar width", () => {
    expect(barDefaults.maxBarSize).toBeLessThanOrEqual(60);
  });
});

describe("areaDefaults", () => {
  it("uses monotone interpolation", () => {
    expect(areaDefaults.type).toBe("monotone");
  });

  it("has subtle fill opacity", () => {
    expect(areaDefaults.fillOpacity).toBeLessThan(0.5);
  });
});

// ---------------------------------------------------------------------------
// Motion
// ---------------------------------------------------------------------------

describe("chartMotionProps", () => {
  it("has animation enabled by default in jsdom", () => {
    // jsdom matchMedia returns false for reduce-motion → animation active
    expect(chartMotionProps.isAnimationActive).toBe(true);
  });

  it("uses reasonable animation duration", () => {
    expect(chartMotionProps.animationDuration).toBeGreaterThan(0);
    expect(chartMotionProps.animationDuration).toBeLessThanOrEqual(1000);
  });

  it("uses ease-out easing", () => {
    expect(chartMotionProps.animationEasing).toBe("ease-out");
  });
});
