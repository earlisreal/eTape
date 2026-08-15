import { useEffect, useRef, useState } from "react";
import type { KeyboardEvent, MouseEvent, RefObject } from "react";

export type ResizableColumn = {
  col: string;
  label: string;
  defaultWidth: number;
  minWidth?: number;
};

type ColumnWidths = Record<string, number>;

const DEFAULT_MIN_WIDTH = 48;

function minWidth(column: ResizableColumn): number {
  return column.minWidth ?? DEFAULT_MIN_WIDTH;
}

function readWidths(settings: Record<string, unknown>, settingsKey: string, columns: readonly ResizableColumn[]): ColumnWidths {
  const raw = settings[settingsKey];
  const saved = raw && typeof raw === "object" ? raw as Record<string, unknown> : {};
  return Object.fromEntries(columns.map((column) => {
    const value = saved[column.col];
    const width = typeof value === "number" && Number.isFinite(value) ? value : column.defaultWidth;
    return [column.col, Math.max(minWidth(column), width)];
  }));
}

function measureCell(cell: HTMLElement): number {
  const probe = cell.cloneNode(true) as HTMLElement;
  const style = getComputedStyle(cell);
  Object.assign(probe.style, {
    position: "fixed",
    left: "-10000px",
    top: "0",
    visibility: "hidden",
    display: "inline-block",
    width: "max-content",
    minWidth: "0",
    maxWidth: "none",
    overflow: "visible",
    whiteSpace: "nowrap",
    font: style.font,
    letterSpacing: style.letterSpacing,
    padding: style.padding,
    border: style.border,
    boxSizing: "border-box",
  });
  document.body.appendChild(probe);
  const width = probe.getBoundingClientRect().width || probe.scrollWidth;
  probe.remove();
  return width;
}

export function useResizableColumns(
  settings: Record<string, unknown>,
  settingsKey: string,
  columns: readonly ResizableColumn[],
  onConfigChange: (patch: Record<string, unknown>) => void,
): {
  tableRef: RefObject<HTMLTableElement | null>;
  widths: ColumnWidths;
  totalWidth: number;
  startResize: (column: string, event: MouseEvent<HTMLSpanElement>) => void;
  autoFit: (column: string) => void;
  onKeyDown: (column: string, event: KeyboardEvent<HTMLSpanElement>) => void;
} {
  const [widths, setWidths] = useState<ColumnWidths>(() => readWidths(settings, settingsKey, columns));
  const widthsRef = useRef(widths);
  widthsRef.current = widths;
  const tableRef = useRef<HTMLTableElement>(null);
  const cleanupRef = useRef<((commit: boolean) => void) | null>(null);

  useEffect(() => () => cleanupRef.current?.(false), []);

  const setLiveWidth = (column: string, width: number) => {
    const next = { ...widthsRef.current, [column]: width };
    widthsRef.current = next;
    setWidths(next);
  };

  const commitWidth = (column: string, width: number) => {
    const next = { ...widthsRef.current, [column]: width };
    widthsRef.current = next;
    setWidths(next);
    onConfigChange({ [settingsKey]: next });
  };

  const findColumn = (column: string): ResizableColumn | undefined => columns.find((c) => c.col === column);

  const autoFit = (column: string) => {
    const definition = findColumn(column);
    if (!definition) return;
    const cells = tableRef.current
      ? Array.from(tableRef.current.querySelectorAll<HTMLElement>("[data-column]"))
        .filter((cell) => cell.dataset.column === column)
      : [];
    const measured = cells.reduce((max, cell) => Math.max(max, measureCell(cell)), 0);
    const fallback = definition.label ? definition.label.length * 8 + 16 : definition.defaultWidth;
    commitWidth(column, Math.max(minWidth(definition), Math.ceil(measured || fallback)));
  };

  const startResize = (column: string, event: MouseEvent<HTMLSpanElement>) => {
    const definition = findColumn(column);
    if (!definition) return;
    cleanupRef.current?.(false);
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = widthsRef.current[column] ?? definition.defaultWidth;
    let finalWidth = startWidth;
    let active = true;

    const onMove = (moveEvent: globalThis.MouseEvent) => {
      finalWidth = Math.max(minWidth(definition), startWidth + moveEvent.clientX - startX);
      setLiveWidth(column, finalWidth);
    };
    const onUp = (commit: boolean) => {
      if (!active) return;
      active = false;
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onMouseUp);
      cleanupRef.current = null;
      if (commit) commitWidth(column, finalWidth);
    };
    const onMouseUp = () => onUp(true);
    cleanupRef.current = onUp;
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onMouseUp);
  };

  const onKeyDown = (column: string, event: KeyboardEvent<HTMLSpanElement>) => {
    const definition = findColumn(column);
    if (!definition) return;
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      event.stopPropagation();
      autoFit(column);
      return;
    }
    const delta = event.key === "ArrowLeft" ? -10 : event.key === "ArrowRight" ? 10 : 0;
    if (!delta) return;
    event.preventDefault();
    event.stopPropagation();
    commitWidth(column, Math.max(minWidth(definition), (widthsRef.current[column] ?? definition.defaultWidth) + delta));
  };

  return {
    tableRef,
    widths,
    totalWidth: columns.reduce((sum, column) => sum + (widths[column.col] ?? column.defaultWidth), 0),
    startResize,
    autoFit,
    onKeyDown,
  };
}

export function ColumnGroup({ columns, widths }: { columns: readonly ResizableColumn[]; widths: ColumnWidths }): JSX.Element {
  return <colgroup>{columns.map((column) => <col key={column.col} style={{ width: widths[column.col] }} />)}</colgroup>;
}

export function ColumnResizeHandle({
  column, width, testId, onMouseDown, onDoubleClick, onKeyDown,
}: {
  column: ResizableColumn;
  width: number;
  testId?: string;
  onMouseDown: (event: MouseEvent<HTMLSpanElement>) => void;
  onDoubleClick: () => void;
  onKeyDown: (event: KeyboardEvent<HTMLSpanElement>) => void;
}): JSX.Element {
  const label = column.label || column.col;
  return <span
    role="separator"
    aria-orientation="vertical"
    aria-label={`Resize ${label} column`}
    aria-valuemin={minWidth(column)}
    aria-valuenow={Math.round(width)}
    tabIndex={0}
    data-testid={testId}
    title="Drag to resize; double-click to auto-fit"
    onMouseDown={(event) => { event.preventDefault(); event.stopPropagation(); onMouseDown(event); }}
    onDoubleClick={(event) => { event.preventDefault(); event.stopPropagation(); onDoubleClick(); }}
    onKeyDown={onKeyDown}
    style={{
      position: "absolute", top: 2, right: -3, bottom: 2, zIndex: 2, width: 6,
      cursor: "col-resize", touchAction: "none", userSelect: "none", opacity: 0.55,
      background: "linear-gradient(90deg, transparent 2px, currentColor 2px, currentColor 4px, transparent 4px)",
    }}
  />;
}
