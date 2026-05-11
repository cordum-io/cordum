import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { RuleFiringSparkline } from "./RuleFiringSparkline";

describe("RuleFiringSparkline", () => {
  it("renders the last-7d firing count with an accessible summary", () => {
    render(<RuleFiringSparkline values={[0, 2, 1, 0, 3, 4, 2]} />);

    expect(screen.getByLabelText("12 firings over the last 7 days")).toBeTruthy();
    expect(screen.getByTestId("rule-firing-sparkline")).toBeTruthy();
    expect(screen.getByText("12")).toBeTruthy();
  });
});
