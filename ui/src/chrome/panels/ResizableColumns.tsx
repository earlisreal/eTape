import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent, MouseEvent, RefCallback } from "react";

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

function fitWidths(baseWidths: ColumnWidths, availableWidth: number | undefined, columns: readonly ResizableColumn[], preserveOverflow: boolean): ColumnWidths {
  const baseTotal = columns.reduce((sum, column) => sum + baseWidths[column.col], 0);
  if (!Number.isFinite(availableWidth) || (availableWidth ?? 0) <= 0 || baseTotal <= 0) return { ...baseWidths };

  const minTotal = columns.reduce((sum, column) => sum + minWidth(column), 0);
  if (preserveOverflow && baseTotal > availableWidth!) return { ...baseWidths };
  if (availableWidth! <= minTotal) {
    return preserveOverflow ? { ...baseWidths } : Object.fromEntries(columns.map((column) => [column.col, minWidth(column)]));
  }

  const result: ColumnWidths = {};
  let remainingWidth = availableWidth!;
  let remainingBase = baseTotal;
  let active = [...columns];
  while (active.length > 0) {
    const scale = remainingWidth / remainingBase;
    const constrained = active.filter((column) => baseWidths[column.col] * scale < minWidth(column));
    if (constrained.length === 0) {
      for (const column of active) result[column.col] = baseWidths[column.col] * scale;
      break;
    }
    for (const column of constrained) {
      result[column.col] = minWidth(column);
      remainingWidth -= result[column.col];
      remainingBase -= baseWidths[column.col];
    }
    active = active.filter((column) => !constrained.includes(column));
  }
  return result;
}

function resizeWidths(current: ColumnWidths, columns: readonly ResizableColumn[], column: string, delta: number): ColumnWidths {
  const targetIndex = columns.findIndex((candidate) => candidate.col === column);
  if (targetIndex < 0) return current;
  const neighborIndex = targetIndex === columns.length - 1 ? targetIndex - 1 : targetIndex + 1;
  const target = columns[targetIndex];
  const targetWidth = current[target.col] ?? target.defaultWidth;
  if (delta < 0) {
    const applied = Math.max(delta, minWidth(target) - targetWidth);
    if (neighborIndex < 0) return { ...current, [column]: targetWidth + applied };
    const neighbor = columns[neighborIndex];
    const neighborWidth = current[neighbor.col] ?? neighbor.defaultWidth;
    return { ...current, [target.col]: targetWidth + applied, [neighbor.col]: neighborWidth - applied };
  }

  const next = { ...current, [target.col]: targetWidth + delta };
  const donorIndexes = [neighborIndex, ...columns.map((_, index) => index).filter((index) => index !== targetIndex && index !== neighborIndex)]
    .filter((index) => index >= 0);
  let remaining = delta;
  for (const donorIndex of donorIndexes) {
    const donor = columns[donorIndex];
    const donorWidth = current[donor.col] ?? donor.defaultWidth;
    const taken = Math.min(remaining, Math.max(0, donorWidth - minWidth(donor)));
    next[donor.col] = donorWidth - taken;
    remaining -= taken;
    if (remaining <= 0) break;
  }
  return next;
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
  availableWidth?: number,
): {
  tableRef: RefCallback<HTMLTableElement>;
  widths: ColumnWidths;
  totalWidth: number;
  startResize: (column: string, event: MouseEvent<HTMLSpanElement>) => void;
  autoFit: (column: string) => void;
  onKeyDown: (column: string, event: KeyboardEvent<HTMLSpanElement>) => void;
} {
  const [baseWidths, setBaseWidths] = useState<ColumnWidths>(() => readWidths(settings, settingsKey, columns));
  const baseWidthsRef = useRef(baseWidths);
  baseWidthsRef.current = baseWidths;
  const savedWidths = settings[settingsKey];
  const [preserveOverflow, setPreserveOverflow] = useState(() => Boolean(savedWidths && typeof savedWidths === "object" && Object.keys(savedWidths).length > 0));
  const [tableElement, setTableElement] = useState<HTMLTableElement | null>(null);
  const tableRef = useCallback((element: HTMLTableElement | null) => setTableElement(element), []);
  const [containerWidth, setContainerWidth] = useState(0);

  useEffect(() => {
    const container = tableElement?.parentElement;
    if (!container) {
      setContainerWidth(0);
      return;
    }
    const updateWidth = () => setContainerWidth(container.clientWidth);
    updateWidth();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(updateWidth);
    observer.observe(container);
    return () => observer.disconnect();
  }, [tableElement]);

  const effectiveWidth = containerWidth > 0 ? containerWidth : availableWidth;
  const widths = useMemo(() => fitWidths(baseWidths, effectiveWidth, columns, preserveOverflow), [baseWidths, effectiveWidth, columns, preserveOverflow]);
  const widthsRef = useRef(widths);
  widthsRef.current = widths;
  const cleanupRef = useRef<((commit: boolean) => void) | null>(null);

  useEffect(() => () => cleanupRef.current?.(false), []);

  const setLiveWidths = (next: ColumnWidths) => {
    setPreserveOverflow(true);
    baseWidthsRef.current = next;
    setBaseWidths(next);
  };

  const commitWidths = (next: ColumnWidths) => {
    setPreserveOverflow(true);
    baseWidthsRef.current = next;
    setBaseWidths(next);
    onConfigChange({ [settingsKey]: next });
  };

  const findColumn = (column: string): ResizableColumn | undefined => columns.find((c) => c.col === column);

  const autoFit = (column: string) => {
    const definition = findColumn(column);
    if (!definition) return;
    const cells = tableElement
      ? Array.from(tableElement.querySelectorAll<HTMLElement>("[data-column]"))
        .filter((cell) => cell.dataset.column === column)
      : [];
    const measured = cells.reduce((max, cell) => Math.max(max, measureCell(cell)), 0);
    const fallback = definition.label ? definition.label.length * 8 + 16 : definition.defaultWidth;
    commitWidths({ ...baseWidthsRef.current, [column]: Math.max(minWidth(definition), Math.ceil(measured || fallback)) });
  };

  const startResize = (column: string, event: MouseEvent<HTMLSpanElement>) => {
    const definition = findColumn(column);
    if (!definition) return;
    cleanupRef.current?.(false);
    event.preventDefault();
    const startX = event.clientX;
    const startWidths = { ...widthsRef.current };
    let finalWidths = { ...startWidths };
    let active = true;

    const onMove = (moveEvent: globalThis.MouseEvent) => {
      finalWidths = resizeWidths(startWidths, columns, column, moveEvent.clientX - startX);
      setLiveWidths(finalWidths);
    };
    const onUp = (commit: boolean) => {
      if (!active) return;
      active = false;
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onMouseUp);
      cleanupRef.current = null;
      if (commit) commitWidths(finalWidths);
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
    commitWidths(resizeWidths(widthsRef.current, columns, column, delta));
  };

  return {
    tableRef,
    widths,
    totalWidth: Math.round(columns.reduce((sum, column) => sum + (widths[column.col] ?? column.defaultWidth), 0)),
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
      position: "absolute", top: 2, right: 0, bottom: 2, zIndex: 2, width: 6,
      cursor: "col-resize", touchAction: "none", userSelect: "none", opacity: 0.55,
      background: "linear-gradient(90deg, transparent 2px, currentColor 2px, currentColor 4px, transparent 4px)",
    }}
  />;
}
