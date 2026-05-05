import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders } from "@/test-utils/render";
import { InputRuleEditorDrawer } from "./InputRuleEditorDrawer";

describe("InputRuleEditorDrawer a11y", () => {
  it("closes on Escape (read-only branch — useDialogA11y wired up)", () => {
    const onClose = vi.fn();
    renderWithProviders(
      <InputRuleEditorDrawer
        open
        readOnly
        rule={null}
        nextRuleIndex={0}
        existingRuleIds={[]}
        onClose={onClose}
        onSave={vi.fn()}
      />,
    );

    fireEvent.keyDown(document, { key: "Escape" });

    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
