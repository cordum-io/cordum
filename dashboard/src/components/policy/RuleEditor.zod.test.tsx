import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

const { policyRulesQuery, jobsQuery } = vi.hoisted(() => ({
  policyRulesQuery: { data: { items: [] } },
  jobsQuery: { data: { items: [] }, isLoading: false },
}));

vi.mock("../../hooks/usePolicies", () => ({
  usePolicyRules: () => policyRulesQuery,
}));
vi.mock("../../hooks/useJobs", () => ({ useJobs: () => jobsQuery }));
vi.mock("@monaco-editor/react", () => ({
  default: ({ value }: { value?: string }) => (
    <pre data-testid="rule-yaml-preview">{value}</pre>
  ),
}));

import { RuleEditor, type RuleEditorSaveData } from "./RuleEditor";

afterEach(cleanup);

function submitForm(container: HTMLElement) {
  const form = container.querySelector("form");
  if (!form) throw new Error("Rule editor form was not rendered");
  fireEvent.submit(form);
}

describe("RuleEditor Zod input/output behavior", () => {
  it("updates the partial YAML preview from raw priority input while Reason is invalid", async () => {
    const onSave = vi.fn<(data: RuleEditorSaveData) => void>();
    const { container } = render(
      <RuleEditor onSave={onSave} onCancel={vi.fn()} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Advanced Options" }));
    fireEvent.change(screen.getByLabelText("Priority"), {
      target: { value: "17" },
    });
    submitForm(container);

    await screen.findByText("Reason is required");
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Reason")).toHaveProperty("value", "");

    fireEvent.change(screen.getByLabelText("Priority"), {
      target: { value: "23" },
    });
    expect(screen.getByLabelText("Priority")).toHaveProperty("value", "23");
    await waitFor(() => {
      expect(screen.getByTestId("rule-yaml-preview").textContent).toContain(
        "priority: 23",
      );
    });
    expect(onSave).not.toHaveBeenCalled();
  });

  it("submits numeric priority and throttle output from browser strings", async () => {
    const onSave = vi.fn<(data: RuleEditorSaveData) => void>();
    const { container } = render(
      <RuleEditor onSave={onSave} onCancel={vi.fn()} />,
    );
    fireEvent.change(screen.getByPlaceholderText("Add value..."), {
      target: { value: "lookup" },
    });
    fireEvent.keyDown(screen.getByPlaceholderText("Add value..."), {
      key: "Enter",
      code: "Enter",
    });
    fireEvent.click(
      screen.getByRole("button", { name: /^Throttle$/ }),
    );
    fireEvent.change(screen.getByLabelText("Max per minute"), {
      target: { value: "75" },
    });
    fireEvent.change(screen.getByLabelText("Burst limit (optional)"), {
      target: { value: "10" },
    });
    fireEvent.change(screen.getByLabelText("Reason"), {
      target: { value: "Limit expensive lookups" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Advanced Options" }));
    fireEvent.change(screen.getByLabelText("Priority"), {
      target: { value: "20" },
    });
    submitForm(container);

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledExactlyOnceWith({
        matchCriteria: { capabilities: ["lookup"], riskTags: [], logic: "AND" },
        logic: "AND",
        decisionType: "throttle",
        reason: "Limit expensive lookups",
        priority: 20,
        throttleConfig: { maxPerMinute: 75, burstLimit: 10 },
      });
    });
  });

  it("shows the rate validation error and prevents invalid submission", async () => {
    const onSave = vi.fn<(data: RuleEditorSaveData) => void>();
    const { container } = render(
      <RuleEditor onSave={onSave} onCancel={vi.fn()} />,
    );
    fireEvent.click(
      screen.getByRole("button", { name: /^Throttle$/ }),
    );
    fireEvent.change(screen.getByLabelText("Reason"), {
      target: { value: "Limit expensive lookups" },
    });
    fireEvent.change(screen.getByLabelText("Max per minute"), {
      target: { value: "0" },
    });
    fireEvent.change(screen.getByLabelText("Burst limit (optional)"), {
      target: { value: "10" },
    });
    submitForm(container);

    await screen.findByText(/Too small: expected number to be >=1/);
    expect(onSave).not.toHaveBeenCalled();
  });
});
