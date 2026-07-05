import { useState } from "react";
import { useTheme } from "./ThemeProvider";
import { LinkGroups, type LinkGroup } from "./linkGroups";

const GROUPS: Exclude<LinkGroup, null>[] = ["red", "green", "blue", "yellow"];

// Already-qualified symbols ("HK.00700", "US.NVDA") carry their own market
// prefix; a bare ticker gets the project-wide default market (US.) so it
// matches the `US.<TICKER>` convention every store/fixture keys by.
//
// This must be an explicit allow-list, not an open-ended `/^[A-Z]+\./` (or
// even a fixed-length `/^[A-Z]{2}\./`) pattern: real US tickers contain a
// dot as part of the ticker itself (BRK.B, BRK.A, BF.B, BF.A), and any
// leading-letters-then-dot rule misclassifies them as already market-
// qualified, leaving them unprefixed.
const MARKET_PREFIXES = ["US.", "HK."];
export function normalizeSymbol(raw: string): string {
  const upper = raw.toUpperCase();
  return MARKET_PREFIXES.some((p) => upper.startsWith(p)) ? raw : `US.${raw}`;
}

// Minimal v1 header: one type-to-focus symbol box per link group + a theme toggle.
export function WorkspaceHeader({ workspaceName, linkGroups }: { workspaceName: string; linkGroups: LinkGroups }): JSX.Element {
  const { palette, mode, setMode } = useTheme();
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "4px 10px",
      background: palette.surface, borderBottom: `1px solid ${palette.border}`, color: palette.text, fontSize: 12 }}>
      <strong style={{ textTransform: "capitalize" }}>{workspaceName}</strong>
      {GROUPS.map((g) => <GroupBox key={g} group={g} linkGroups={linkGroups} palette={palette} />)}
      <span style={{ flex: 1 }} />
      <button aria-label="toggle theme" onClick={() => setMode(mode === "light" ? "dark" : "light")}>
        {mode === "light" ? "◐ dark theme" : "◑ light theme"}
      </button>
    </div>
  );
}

function GroupBox({ group, linkGroups, palette }: { group: Exclude<LinkGroup, null>; linkGroups: LinkGroups; palette: import("../render/palette").Palette }): JSX.Element {
  const [text, setText] = useState("");
  const swatch = { red: palette.linkRed, green: palette.linkGreen, blue: palette.linkBlue, yellow: palette.linkYellow }[group];
  return (
    <span style={{ display: "flex", alignItems: "center", gap: 4 }}>
      <span style={{ width: 8, height: 8, borderRadius: 2, background: swatch }} />
      <input aria-label={`focus ${group}`} value={text} placeholder="symbol"
        onChange={(e) => setText(e.target.value.toUpperCase())}
        onKeyDown={(e) => { if (e.key === "Enter" && text.trim()) linkGroups.focus(group, normalizeSymbol(text.trim())); }}
        style={{ width: 84, fontSize: 12 }} />
    </span>
  );
}
