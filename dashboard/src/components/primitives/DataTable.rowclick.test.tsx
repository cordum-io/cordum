import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React, { act, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { DataTable, type DataTableProps } from "./DataTable";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface Row {
  id: string;
  name: string;
}

type Columns = DataTableProps<Row>["columns"];
type ControlKind = "button" | "link" | "data-row-action";

const data: Row[] = [
  { id: "r1", name: "Alpha" },
  { id: "r2", name: "Bravo" },
];

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

function render(columns: Columns, onRowClick: (row: Row) => void) {
  const props: DataTableProps<Row> = { columns, data, emptyState: <div>empty</div>, onRowClick };
  act(() => {
    root.render(React.createElement(DataTable<Row>, props));
  });
}

function cellColumns(cell: (row: Row) => ReactElement): Columns {
  return [{ accessorKey: "name", header: "Name", cell: ({ row }) => cell(row.original) }];
}

function byTestId(testId: string): Element {
  const el = container.querySelector(`[data-testid="${testId}"]`);
  expect(el).not.toBeNull();
  return el as Element;
}

function click(el: Element) {
  act(() => {
    el.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
}

function Icon({ id, kind }: { id: string; kind: string }) {
  return (
    <svg data-testid={`${kind}-svg-${id}`} aria-hidden="true" viewBox="0 0 10 10">
      <path data-testid={`${kind}-path-${id}`} d="M0 0h10v10H0z" />
    </svg>
  );
}

// Icon-only controls carry an aria-label: lint:a11y treats anchor-has-content as an error.
function control(kind: ControlKind, inner: () => void, icon: ReactElement): ReactElement {
  if (kind === "button") {
    return (
      <button type="button" aria-label="Act" onClick={inner}>
        {icon}
      </button>
    );
  }
  if (kind === "link") {
    // A fragment href: jsdom navigates by hash silently, so the click needs no preventDefault.
    return (
      <a href="#datatable-rowclick" aria-label="Open" onClick={inner}>
        {icon}
      </a>
    );
  }
  return (
    <span data-row-action="" role="presentation" onClick={inner}>
      {icon}
    </span>
  );
}

describe("primitives/DataTable row click and SVG targets", () => {
  it.each<ControlKind>(["button", "link", "data-row-action"])(
    "does not fire onRowClick for a click on an icon <path> inside a %s",
    (kind) => {
      const onRowClick = vi.fn();
      const inner = vi.fn();
      render(
        cellColumns((row) => control(kind, inner, <Icon id={row.id} kind={kind} />)),
        onRowClick,
      );

      const path = byTestId(`${kind}-path-r2`);
      expect(path instanceof SVGElement).toBe(true);
      expect(path instanceof HTMLElement).toBe(false);
      click(path);

      expect(inner).toHaveBeenCalledTimes(1);
      expect(onRowClick).not.toHaveBeenCalled();
    },
  );

  it("does not fire onRowClick for a click on the <svg> root inside a button", () => {
    const onRowClick = vi.fn();
    const inner = vi.fn();
    render(
      cellColumns((row) => control("button", inner, <Icon id={row.id} kind="button" />)),
      onRowClick,
    );

    const svg = byTestId("button-svg-r2");
    expect(svg instanceof SVGElement).toBe(true);
    expect(svg instanceof HTMLElement).toBe(false);
    click(svg);

    expect(inner).toHaveBeenCalledTimes(1);
    expect(onRowClick).not.toHaveBeenCalled();
  });

  it("fires onRowClick once with that row for a decorative icon outside any control", () => {
    const onRowClick = vi.fn();
    render(
      cellColumns((row) => <Icon id={row.id} kind="plain" />),
      onRowClick,
    );

    const path = byTestId("plain-path-r2");
    expect(path instanceof SVGElement).toBe(true);
    click(path);

    expect(onRowClick).toHaveBeenCalledTimes(1);
    expect(onRowClick).toHaveBeenCalledWith(data[1]);
  });

  it("fires onRowClick once with that row for an ordinary text cell", () => {
    const onRowClick = vi.fn();
    render([{ accessorKey: "name", header: "Name" }], onRowClick);

    const rows = container.querySelectorAll<HTMLTableRowElement>("tbody > tr");
    expect(rows.length).toBe(2);
    const cell = rows[1].querySelector("td");
    expect(cell?.textContent).toBe("Bravo");
    click(cell as Element);

    expect(onRowClick).toHaveBeenCalledTimes(1);
    expect(onRowClick).toHaveBeenCalledWith(data[1]);
  });
});
