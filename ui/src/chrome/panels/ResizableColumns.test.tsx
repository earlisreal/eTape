// @vitest-environment jsdom
import { describe, it, expect } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ColumnGroup, ColumnResizeHandle, useResizableColumns, type ResizableColumn } from "./ResizableColumns";

const COLUMNS: ResizableColumn[] = [
  { col: "symbol", label: "Symbol", defaultWidth: 100, minWidth: 48 },
  { col: "qty", label: "Qty", defaultWidth: 80, minWidth: 48 },
];

function Harness({ settings = {}, availableWidth, onConfigChange }: { settings?: Record<string, unknown>; availableWidth?: number; onConfigChange: (patch: Record<string, unknown>) => void }) {
  const resize = useResizableColumns(settings, "columnWidths", COLUMNS, onConfigChange, availableWidth);
  return <table ref={resize.tableRef} data-total-width={resize.totalWidth}>
    <ColumnGroup columns={COLUMNS} widths={resize.widths} />
    <thead><tr>{COLUMNS.map((column) => <th key={column.col} data-column={column.col}>
      {column.label}
      <ColumnResizeHandle column={column} width={resize.widths[column.col]} testId={`resize-${column.col}`}
        onMouseDown={(event) => resize.startResize(column.col, event)} onDoubleClick={() => resize.autoFit(column.col)}
        onKeyDown={(event) => resize.onKeyDown(column.col, event)} />
    </th>)}</tr></thead>
    <tbody><tr><td data-column="symbol">US.AAPL</td><td data-column="qty">10</td></tr></tbody>
  </table>;
}

describe("ResizableColumns", () => {
  it("persists a dragged width once on mouseup and clamps the minimum", () => {
    const changes: Array<Record<string, unknown>> = [];
    render(<Harness onConfigChange={(patch) => changes.push(patch)} />);
    const handle = screen.getByTestId("resize-symbol");

    fireEvent.mouseDown(handle, { clientX: 100 });
    fireEvent.mouseMove(window, { clientX: 120 });
    fireEvent.mouseUp(window);
    expect(changes.at(-1)).toEqual({ columnWidths: { symbol: 120, qty: 60 } });

    fireEvent.mouseDown(handle, { clientX: 100 });
    fireEvent.mouseMove(window, { clientX: -500 });
    fireEvent.mouseUp(window);
    expect(changes.at(-1)).toEqual({ columnWidths: { symbol: 48, qty: 132 } });
  });

  it("fills a wide panel, compresses a narrow panel, then scrolls at the minimum", () => {
    const changes: Array<Record<string, unknown>> = [];
    const { rerender } = render(<Harness availableWidth={300} onConfigChange={(patch) => changes.push(patch)} />);
    expect(screen.getByTestId("resize-symbol").getAttribute("aria-valuenow")).toBe("167");
    expect(screen.getByTestId("resize-qty").getAttribute("aria-valuenow")).toBe("133");
    expect(Number(screen.getByRole("table").getAttribute("data-total-width"))).toBeCloseTo(300, 5);

    rerender(<Harness availableWidth={120} onConfigChange={(patch) => changes.push(patch)} />);
    expect(screen.getByTestId("resize-symbol").getAttribute("aria-valuenow")).toBe("67");
    expect(screen.getByTestId("resize-qty").getAttribute("aria-valuenow")).toBe("53");
    expect(Number(screen.getByRole("table").getAttribute("data-total-width"))).toBeCloseTo(120, 5);

    rerender(<Harness availableWidth={80} onConfigChange={(patch) => changes.push(patch)} />);
    expect(screen.getByTestId("resize-symbol").getAttribute("aria-valuenow")).toBe("48");
    expect(screen.getByTestId("resize-qty").getAttribute("aria-valuenow")).toBe("48");
    expect(Number(screen.getByRole("table").getAttribute("data-total-width"))).toBeCloseTo(96, 5);
  });

  it("restores saved widths and auto-fits on double-click", () => {
    const changes: Array<Record<string, unknown>> = [];
    render(<Harness settings={{ columnWidths: { symbol: 220 } }} onConfigChange={(patch) => changes.push(patch)} />);
    expect(screen.getByTestId("resize-symbol").getAttribute("aria-valuenow")).toBe("220");

    fireEvent.doubleClick(screen.getByTestId("resize-symbol"));
    expect(changes.at(-1)).toEqual({ columnWidths: { symbol: 64, qty: 80 } });
  });
});
