import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { formatTapeTime } from "../../render/format";

/** Classifies a news item's seen_at as "today" (bronze fresh treatment) vs an older, muted date. */
export function newsDateLabel(seenAtISO: string, nowMs: number): { label: string; today: boolean } {
  const d = new Date(seenAtISO);
  const now = new Date(nowMs);
  const sameDay = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  if (sameDay) return { label: "today", today: true };
  return { label: d.toLocaleDateString("en-US", { month: "short", day: "numeric" }), today: false };
}

export function NewsPanel({ config, stores, linkGroups }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const snap = useSyncExternalStore((cb) => stores.news.subscribe(cb), () => stores.news.getSnapshot());
  const [symbol, setSymbol] = useState<string | undefined>(() => linkGroups.symbolFor(config.group));
  useEffect(() => {
    setSymbol(linkGroups.symbolFor(config.group));
    return linkGroups.subscribe(() => setSymbol(linkGroups.symbolFor(config.group)));
  }, [linkGroups, config.group]);
  const items = useMemo(() => (symbol ? stores.news.itemsFor(symbol) : []), [snap, symbol, stores.news]);

  return (
    <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      {/* Reserved slot for high-salience halt banners (v2 feed) — empty in v1. */}
      <div data-testid="halt-slot" />
      <div style={{ padding: "6px 8px", fontWeight: 600, borderBottom: `1px solid ${palette.border}` }}>
        {symbol ? `News · ${symbol}` : "News · no symbol focused"}
      </div>
      {symbol && items.length === 0 && (
        <div style={{ padding: 12, color: palette.textMuted }}>No news for {symbol}.</div>
      )}
      {items.map((it, i) => {
        const { label, today } = newsDateLabel(it.seen_at, Date.now());
        return (
          <div key={it.url || `${it.headline}-${i}`}
            style={{
              padding: "6px 8px", borderBottom: `1px solid ${palette.border}`,
              ...(today ? { background: "rgba(154,106,27,.08)", boxShadow: "inset 2px 0 0 var(--accent)" } : {}),
            }}>
            <a href={it.url} onClick={(e) => { e.preventDefault(); window.open(it.url, "_blank", "noopener,noreferrer"); }}
              style={{ color: palette.accent, textDecoration: "none", cursor: "pointer" }}>{it.headline}</a>
            <div className="mono" style={{ marginTop: 2 }}>
              <span style={{ color: today ? palette.accent : palette.textMuted }}>{label}</span>
              <span style={{ color: palette.textMuted }}> · {formatTapeTime(it.seen_at)} · {it.source}</span>
            </div>
          </div>
        );
      })}
    </div>
  );
}
