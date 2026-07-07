import { useState } from "react";
import { useTheme } from "./ThemeProvider";
import { LinkGroups, type LinkGroup } from "./linkGroups";
import { normalizeSymbol } from "./symbol";

export { normalizeSymbol } from "./symbol";

const GROUPS: Exclude<LinkGroup, null>[] = ["red", "green", "blue", "yellow"];

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
