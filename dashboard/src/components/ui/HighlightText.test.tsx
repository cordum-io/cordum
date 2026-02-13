import { afterEach, beforeEach, describe, expect, it } from "vitest";

import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { HighlightText } from "./HighlightText";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function render(props: { text: string; query: string; className?: string }) {
  act(() => {
    root.render(React.createElement(HighlightText, props));
  });
}

describe("HighlightText", () => {
  it("renders plain text when query is empty", () => {
    render({ text: "Hello World", query: "" });
    expect(container.textContent).toBe("Hello World");
    expect(container.querySelectorAll("mark")).toHaveLength(0);
  });

  it("renders plain text when query is whitespace", () => {
    render({ text: "Hello World", query: "   " });
    expect(container.textContent).toBe("Hello World");
    expect(container.querySelectorAll("mark")).toHaveLength(0);
  });

  it("highlights matching substring", () => {
    render({ text: "Hello World", query: "World" });
    const marks = container.querySelectorAll("mark");
    expect(marks).toHaveLength(1);
    expect(marks[0].textContent).toBe("World");
    expect(marks[0].className).toContain("bg-amber-200");
  });

  it("is case-insensitive", () => {
    render({ text: "Error occurred in Error handler", query: "error" });
    const marks = container.querySelectorAll("mark");
    expect(marks).toHaveLength(2);
    expect(marks[0].textContent).toBe("Error");
    expect(marks[1].textContent).toBe("Error");
  });

  it("highlights multiple occurrences", () => {
    render({ text: "foo bar foo baz foo", query: "foo" });
    const marks = container.querySelectorAll("mark");
    expect(marks).toHaveLength(3);
  });

  it("handles special regex characters in query", () => {
    render({ text: "price is $100.00 (USD)", query: "$100.00" });
    const marks = container.querySelectorAll("mark");
    expect(marks).toHaveLength(1);
    expect(marks[0].textContent).toBe("$100.00");
  });

  it("applies custom className to wrapper span", () => {
    render({ text: "test", query: "", className: "custom-class" });
    const span = container.querySelector("span");
    expect(span?.className).toContain("custom-class");
  });

  it("preserves full text content when highlighting", () => {
    render({ text: "abc def abc", query: "abc" });
    expect(container.textContent).toBe("abc def abc");
  });
});
