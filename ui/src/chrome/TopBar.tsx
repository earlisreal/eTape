import { LatencyReadout } from "./LatencyReadout";
import { SessionClock } from "./SessionClock";
import type { HealthStore } from "../data/HealthStore";
import { useTheme } from "./ThemeProvider";
import { Button } from "./controls/Button";
import type { LinkGroup } from "./linkGroups";
import type { HotkeyTarget } from "./hotkeyTarget";
import type { VenueID } from "../wire/contract";
import { bareSymbol } from "./exec/orderStatus";

// Padlock state icon for the arm/disarm chip. stroke="currentColor" so it
// inherits the chip's state color automatically (no separate color prop) —
// same convention as ./panels/tv/tvIcons.tsx. open=true (armed → trading is
// currently unlocked) draws the shackle swung open; open=false (disarmed →
// currently locked) draws the shackle closed. aria-hidden + no text nodes so
// it never affects the chip's accessible text content.
function LockIcon({ open }: { open: boolean }): JSX.Element {
  return (
    <svg width={14} height={14} viewBox="0 0 24 24" fill="none"
      stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round"
      aria-hidden="true" focusable="false">
      <rect x="5" y="11" width="14" height="9" rx="2" />
      {open ? <path d="M8 11V7a4 4 0 0 1 7-3" /> : <path d="M8 11V7a4 4 0 0 1 8 0v4" />}
    </svg>
  );
}

export interface TopBarProps {
  workspaceName: string;
  health: HealthStore;
  armed: boolean;
  onArmToggle: () => void;
  onAddPanel: () => void;
  onNewWindow: () => void;
  onOpenSettings: () => void;
  onOpenConnection: () => void;
  onOpenPractice: () => void;
  targetCue?: HotkeyTargetCue;
}

export type HotkeyTargetCueState = "ready" | "no-target" | "ungrouped" | "missing-symbol" | "missing-venue";
export interface HotkeyTargetCue {
  state: HotkeyTargetCueState;
  group: Exclude<LinkGroup, null> | null;
  symbol?: string;
  venue?: VenueID;
}

export function targetCueFor(target: HotkeyTarget | null): HotkeyTargetCue {
  if (!target) return { state: "no-target", group: null };
  if (!target.group) return {
    state: "ungrouped", group: null,
    ...(target.symbol === undefined ? {} : { symbol: target.symbol }),
    ...(target.venue === undefined ? {} : { venue: target.venue }),
  };
  if (!target.symbol) return {
    state: "missing-symbol", group: target.group,
    ...(target.venue === undefined ? {} : { venue: target.venue }),
  };
  if (!target.venue) return { state: "missing-venue", group: target.group, symbol: target.symbol };
  return { state: "ready", group: target.group, symbol: target.symbol, venue: target.venue };
}

function TargetCue({ cue, palette }: { cue: HotkeyTargetCue; palette: ReturnType<typeof useTheme>["palette"] }): JSX.Element {
  const dot = cue.group === null ? palette.textMuted : {
    red: palette.linkRed, green: palette.linkGreen, blue: palette.linkBlue, yellow: palette.linkYellow,
  }[cue.group];
  const symbol = cue.symbol ? bareSymbol(cue.symbol) : "—";
  const venue = cue.venue ?? "—";
  const reason = {
    ready: "",
    "no-target": "no target",
    ungrouped: "ungrouped panel",
    "missing-symbol": "no symbol",
    "missing-venue": "no venue",
  }[cue.state];
  const label = cue.state === "ready"
    ? `${symbol} · ${venue}`
    : `Hotkeys blocked · ${reason}${cue.state === "no-target" ? "" : ` · ${symbol} · ${venue}`}`;
  return (
    <span className="top-bar-target-cue" data-testid="hotkey-target-cue" data-state={cue.state} role="status"
      aria-label={label} title={label}
      style={{ display: "inline-flex", alignItems: "center", gap: 5, minWidth: 0,
        color: cue.state === "ready" ? palette.textMuted : palette.danger, fontSize: 11, whiteSpace: "nowrap" }}>
      <span data-testid="hotkey-target-dot" aria-hidden="true"
        style={{ width: 7, height: 7, borderRadius: "50%", background: dot, flex: "0 0 auto" }} />
      <span className="top-bar-target-label">{label}</span>
    </span>
  );
}

// Daylight Ledger top bar: eTape wordmark + workspace name + connection latency +
// live ET clock/countdown on the left, the hotkey target dead-center, and shell
// actions (add panel / new window / settings) + the arm/disarm chip on the right.
export function TopBar(p: TopBarProps): JSX.Element {
  const { palette } = useTheme();
  const cue = p.targetCue ?? targetCueFor(null);
  return (
    <div className="top-bar" data-testid="top-bar" style={{ display: "grid", gridTemplateColumns: "minmax(0, 1fr) auto minmax(0, 1fr)", alignItems: "center", gap: 10,
      padding: "7px 12px", background: palette.surface, borderBottom: `1px solid ${palette.border}` }}>
      <div className="top-bar-left" style={{ display: "flex", alignItems: "center", gap: 10, minWidth: 0 }}>
        <span className="serif" style={{ fontWeight: 600, fontSize: 14 }}>eTape</span>
        {p.workspaceName !== "main" && <span className="top-bar-workspace" style={{ color: palette.textMuted }}>· {p.workspaceName}</span>}
        <span className="top-bar-latency"><LatencyReadout health={p.health} onOpen={p.onOpenConnection} /></span>
        <SessionClock />
      </div>
      <div className="top-bar-target" style={{ display: "flex", alignItems: "center", justifyContent: "center", minWidth: 0, maxWidth: "100%" }}>
        <TargetCue cue={cue} palette={palette} />
      </div>
      <div className="top-bar-actions" style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", gap: 10, minWidth: 0 }}>
        <Button onClick={p.onAddPanel}>+ Add panel</Button>
        <Button onClick={p.onNewWindow}>⧉ New window</Button>
        <Button aria-label="Practice" title="Practice: synthetic demo market" onClick={p.onOpenPractice}>▶ Practice</Button>
        <Button aria-label="Settings" onClick={p.onOpenSettings}>⚙ Settings</Button>
        <Button data-testid="arm-chip" onClick={p.onArmToggle}
          style={{ fontWeight: 600, letterSpacing: ".08em",
            // Fixed width sized to the longer "UNLOCK TRADING" label (measured
            // 149px) so toggling the shorter "LOCK TRADING" label doesn't
            // resize the button and shift the other header buttons.
            width: 154,
            color: p.armed ? palette.accent : palette.textMuted,
            borderColor: p.armed ? palette.accent : palette.borderStrong,
            background: p.armed ? "rgba(154,106,27,.12)" : "rgba(106,114,128,.12)" }}
          // The chip's color AND icon (LockIcon above) both track the
          // armed/disarmed state; the label is the click action — the inverse
          // of that state — so a reader must not assume the label names the
          // current state. The chip's color/border/background ARE the
          // armed/disarmed state indicator — a permanent inline background
          // that would permanently defeat a plain CSS :hover rule (see
          // HoverButton's own doc comment; Button.tsx wraps it for exactly
          // this kind of override). Rather than washing to the default
          // neutral overlay, hover adds an inset ring in the SAME state color
          // so armed reads brighter/armed and disarmed reads brighter/disarmed,
          // never neutral.
          hoverStyle={{ boxShadow: `inset 0 0 0 1px ${p.armed ? palette.accent : palette.borderStrong}` }}>
          <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
            <LockIcon open={p.armed} />
            {p.armed ? "LOCK TRADING" : "UNLOCK TRADING"}
          </span>
        </Button>
      </div>
    </div>
  );
}
