import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { ScannerSession } from "../../wire/contract";
import type { LinkGroup } from "../linkGroups";
import { useTheme } from "../ThemeProvider";
import { formatTapeTime } from "../../render/format";
import { formatChangePct, formatCompactShares, msUntilEtMidnight } from "../format";
import { applyScannerFilters, sortByChangeDesc, type ScannerThresholds } from "./scannerFilter";

const SESSION_LABEL: Record<ScannerSession, string> = {
  premarket: "Pre-market", rth: "RTH movers", afterhours: "After-hours",
};
const GROUPS: Exclude<LinkGroup, null>[] = ["red", "green", "blue", "yellow"];

function readThresholds(s: Record<string, unknown>): ScannerThresholds {
  const t = (s.thresholds ?? {}) as Partial<ScannerThresholds>;
  return {
    minChangePct: typeof t.minChangePct === "number" ? t.minChangePct : 0,
    floatCapShares: typeof t.floatCapShares === "number" ? t.floatCapShares : null,
    minVolume: typeof t.minVolume === "number" ? t.minVolume : 0,
  };
}

export function ScannerPanel(
  { config, stores, linkGroups, onConfigChange, session }: PanelProps & { session: ScannerSession },
): JSX.Element {
  const { palette } = useTheme();
  const snap = useSyncExternalStore((cb) => stores.scanner.subscribe(cb), () => stores.scanner.getSnapshot());
  const sv = useMemo(() => stores.scanner.view(session), [snap, session, stores.scanner]);
  const [thresholds, setThresholds] = useState<ScannerThresholds>(() => readThresholds(config.settings));
  const targetGroup = ((config.settings.targetGroup as LinkGroup) ?? "green") as Exclude<LinkGroup, null>;

  // ET-midnight dedup reset: clear the per-session seen-set so the next session's
  // first prints flash fresh. Re-arms after each fire.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout>;
    const arm = () => { timer = setTimeout(() => { stores.scanner.resetSeen(session); arm(); }, msUntilEtMidnight(new Date())); };
    arm();
    return () => clearTimeout(timer);
  }, [stores.scanner, session]);

  const rows = useMemo(() => sortByChangeDesc(applyScannerFilters(sv.rows, thresholds)), [sv.rows, thresholds]);

  const updateThreshold = (patch: Partial<ScannerThresholds>) => {
    const next = { ...thresholds, ...patch };
    setThresholds(next);
    onConfigChange({ ...config.settings, thresholds: next });
  };
  const swatch = (g: Exclude<LinkGroup, null>): string =>
    ({ red: palette.linkRed, green: palette.linkGreen, blue: palette.linkBlue, yellow: palette.linkYellow }[g]);
  const header = sv.refreshedAt
    ? `${SESSION_LABEL[session]} · updated ${formatTapeTime(sv.refreshedAt)}`
    : `Waiting for ${SESSION_LABEL[session].toLowerCase()} data…`;

  const th = { padding: "2px 8px", position: "sticky" as const, top: 0, background: palette.surface };
  return (
    <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 8px", borderBottom: `1px solid ${palette.border}` }}>
        <span style={{ fontWeight: 600 }}>{header}</span>
        <span style={{ flex: 1 }} />
        {GROUPS.map((g) => (
          <button key={g} title={`Send clicks to ${g}`} aria-label={`send clicks to ${g}`}
            onClick={() => onConfigChange({ ...config.settings, targetGroup: g })}
            style={{ width: 14, height: 14, borderRadius: 3, background: swatch(g), padding: 0, cursor: "pointer",
              border: targetGroup === g ? `2px solid ${palette.text}` : `1px solid ${palette.border}` }} />
        ))}
      </div>
      <div style={{ display: "flex", gap: 10, padding: "4px 8px", color: palette.textMuted, borderBottom: `1px solid ${palette.border}` }}>
        <label>min change % <input aria-label="min change %" type="number" value={thresholds.minChangePct}
          onChange={(e) => updateThreshold({ minChangePct: Number(e.target.value) || 0 })} style={{ width: 52 }} /></label>
        <label>float ≤ <input aria-label="float cap" type="number" value={thresholds.floatCapShares ?? ""}
          onChange={(e) => updateThreshold({ floatCapShares: e.target.value === "" ? null : Number(e.target.value) })} style={{ width: 90 }} /></label>
        <label>vol ≥ <input aria-label="min volume" type="number" value={thresholds.minVolume}
          onChange={(e) => updateThreshold({ minVolume: Number(e.target.value) || 0 })} style={{ width: 80 }} /></label>
      </div>
      <table style={{ width: "100%", borderCollapse: "collapse" }}>
        <thead>
          <tr style={{ color: palette.textMuted, textAlign: "right" }}>
            <th style={{ ...th, textAlign: "left" }}>Symbol</th><th style={th}>%</th><th style={th}>Last</th><th style={th}>Float</th><th style={th}>Vol</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.symbol} onClick={() => linkGroups.focus(targetGroup, r.symbol)}
              style={{ cursor: "pointer", textAlign: "right", opacity: r.muted ? 0.55 : 1,
                background: r.isNewHit ? palette.accent + "33" : "transparent" }}>
              <td style={{ textAlign: "left", padding: "2px 8px" }}>{r.symbol}</td>
              <td style={{ padding: "2px 8px", color: r.changePct === null ? palette.textMuted : r.changePct > 0 ? palette.up : r.changePct < 0 ? palette.down : palette.text }}>{formatChangePct(r.changePct)}</td>
              <td style={{ padding: "2px 8px" }}>{r.last === null ? "—" : r.last.toFixed(2)}</td>
              <td style={{ padding: "2px 8px" }}>{formatCompactShares(r.floatShares)}</td>
              <td style={{ padding: "2px 8px" }}>{formatCompactShares(r.volume)}</td>
            </tr>
          ))}
          {rows.length === 0 && sv.refreshedAt && (
            <tr><td colSpan={5} style={{ padding: 12, color: palette.textMuted, textAlign: "center" }}>No symbols match the current filters.</td></tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
