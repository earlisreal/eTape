import { useContext, type ReactNode, type Ref } from "react";
import { createPortal } from "react-dom";
import { scannerSyncStatusText } from "../scannerSync";
import { PanelHeaderSlotContext } from "./headerSlot";
import type { PanelProps } from "./registry";
import { IconGear, IconVolume, IconVolumeOff } from "./tv/tvIcons";
import "./scanner.css";

export function ScannerHeader({ title, context, muted, onMute, filtersOpen, onFilters, filterTriggerRef, children }: {
  title: string;
  context: string | null;
  muted: boolean;
  onMute: () => void;
  filtersOpen: boolean;
  onFilters: () => void;
  filterTriggerRef: Ref<HTMLButtonElement>;
  children?: ReactNode;
}): JSX.Element | null {
  const headerSlot = useContext(PanelHeaderSlotContext);
  const soundLabel = muted ? "Unmute Scanner sounds" : "Mute Scanner sounds";
  const controls = (
    <div className="scanner-header-controls" data-testid="scanner-header-controls">
      <span className="serif scanner-title">{title}</span>
      {context && <span className="mono scanner-caption" title={context}>· <span>{context}</span></span>}
      <span className="scanner-spacer" />
      <button type="button" className="scanner-header-icon scanner-sound" aria-label={soundLabel}
        aria-pressed={muted} title={soundLabel} onClick={onMute}>
        {muted ? <IconVolumeOff size={13} /> : <IconVolume size={13} />}
      </button>
      <button type="button" className="scanner-header-icon" aria-label="filters" aria-expanded={filtersOpen}
        title="Filters" ref={filterTriggerRef} onClick={onFilters}>
        <IconGear size={13} />
      </button>
      {children}
    </div>
  );
  if (headerSlot === undefined) return controls;
  return headerSlot ? createPortal(controls, headerSlot) : null;
}

export function ScannerSyncControl({ sync, sourceLabel = "Monitoring source", sourceName = "Scanner" }: {
  sync: NonNullable<PanelProps["scannerSync"]>;
  sourceLabel?: string;
  sourceName?: "Scanner" | "List" | "Watchlist";
}): JSX.Element {
  const { status } = sync;
  const syncName = sourceName === "Watchlist" ? "Watchlist Sync" : "Scanner Sync";
  const toggleLabel = `${sync.enabled ? "Disable" : "Enable"} ${syncName}`;
  const waitingForWatchlist = sourceName === "Watchlist" && status.kind === "paused" && status.reason === "source";
  const statusText = (sync.statusVisible ?? sync.selected) && status.kind !== "disabled"
    && (status.targetCount > 0 || status.kind === "paused")
    ? waitingForWatchlist ? "Waiting for Watchlist" : status.kind === "paused" ? "Paused" : `${status.availableCount}/${status.targetCount}`
    : null;
  return (
    <div className="scanner-sync" data-testid="scanner-sync-control">
      {sync.selected ? <>
        <span className="mono scanner-truncate">Sync to Following</span>
        <button type="button" className="mono scanner-sync-toggle" aria-label={toggleLabel}
          aria-pressed={sync.enabled} title={toggleLabel} onClick={sync.onToggle}>
          <span className="scanner-sync-track" aria-hidden="true"><span /></span>
          <span>{sync.enabled ? "ON" : "OFF"}</span>
        </button>
        <span className="scanner-spacer" />
      </> : <>
        <span className="mono scanner-truncate">{sourceLabel}</span>
        <span className="scanner-spacer" />
        <button type="button" className="mono scanner-sync-source" aria-label={`Use this ${sourceName} as Monitoring Source`}
          title={`Use this ${sourceName} as the Monitoring Source`} onClick={sync.onSelect}>Use this {sourceName}</button>
      </>}
      {statusText && <span className="mono scanner-sync-status" aria-live="polite"
        title={waitingForWatchlist ? "Waiting for Watchlist" : scannerSyncStatusText(status)}>{statusText}</span>}
    </div>
  );
}
