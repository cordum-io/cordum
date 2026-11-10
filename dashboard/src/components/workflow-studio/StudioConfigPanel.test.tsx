import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Node } from "reactflow";
import type { UnifiedNodeData } from "./types";

const { packs } = vi.hoisted(() => ({
  packs: [{ id: "pack-1", name: "Example pack", version: "1.0.0", capabilities: ["echo"] }],
}));

vi.mock("../../hooks/useSettings", () => ({ useTopics: () => [] }));
vi.mock("../../hooks/usePacks", () => ({ usePacks: () => ({ data: { items: packs } }) }));
vi.mock("../../hooks/useWorkflows", () => ({ useWorkflows: () => ({ data: [] }) }));

import { StudioConfigPanel } from "./StudioConfigPanel";

afterEach(cleanup);

function node(stepType: string, data: Partial<UnifiedNodeData> = {}): Node<UnifiedNodeData> {
  return {
    id: "step-1",
    position: { x: 0, y: 0 },
    data: { label: "Example", stepId: "step-1", stepType, mode: "edit", ...data },
  };
}

function changeInput(container: HTMLElement, name: string, value: string) {
  const input = container.querySelector<HTMLInputElement>(`input[name="${name}"]`);
  if (!input) throw new Error(`Input ${name} was not rendered`);
  fireEvent.change(input, { target: { value } });
}

describe("StudioConfigPanel parsed form submission", () => {
  it.each([
    { stepType: "agent-task", data: { topic: "job.agent" } },
    { stepType: "pack-action", data: { config: { packId: "pack-1" } } },
    { stepType: "tool-call", data: { config: { capability: "lookup" } } },
  ])("submits numeric retry output from $stepType browser input", async ({ stepType, data }) => {
    const onSave = vi.fn();
    const { container } = render(
      <StudioConfigPanel node={node(stepType, data)} onSave={onSave} onClose={vi.fn()} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Advanced" }));
    changeInput(container, "retryMax", "3");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(onSave).toHaveBeenCalledWith("step-1", expect.objectContaining({
      label: "Example",
      config: expect.objectContaining({ retryMax: 3 }),
    })));
  });

  it("parses generic fan-out concurrency before mapping it to the workflow step", async () => {
    const onSave = vi.fn();
    const { container } = render(
      <StudioConfigPanel
        node={node("fan-out", { for_each: "items" })}
        onSave={onSave}
        onClose={vi.fn()}
      />,
    );
    changeInput(container, "parallelism", "3");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(onSave).toHaveBeenCalledWith("step-1", expect.objectContaining({
      config: expect.objectContaining({ forEach: "items", parallelism: 3 }),
      max_parallel: 3,
    })));
  });

  it("retains edited switch field-array routes in parsed output", async () => {
    const onSave = vi.fn();
    const { container } = render(
      <StudioConfigPanel
        node={node("switch", { input: { cases: [{ match: "first", next: "child" }] } })}
        allNodes={[{ ...node("notify"), id: "child" }]}
        onSave={onSave}
        onClose={vi.fn()}
      />,
    );
    changeInput(container, "switchCases.0.matchValue", "updated");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(onSave).toHaveBeenCalledWith("step-1", expect.objectContaining({
      config: expect.objectContaining({ cases: [{ match: "updated", next: "child" }] }),
    })));
  });

  it("shows the selected generic schema error and prevents invalid submission", async () => {
    const onSave = vi.fn();
    const { container } = render(
      <StudioConfigPanel node={node("delay")} onSave={onSave} onClose={vi.fn()} />,
    );
    changeInput(container, "label", "Updated label");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await screen.findByText("Duration required");
    expect(onSave).not.toHaveBeenCalled();
  });
});
