import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { formatTapeTime } from "../../render/format";

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
      {items.map((it, i) => (
        <div key={it.url || `${it.headline}-${i}`} style={{ padding: "6px 8px", borderBottom: `1px solid ${palette.border}` }}>
          <a href={it.url} onClick={(e) => { e.preventDefault(); window.open(it.url, "_blank", "noopener,noreferrer"); }}
            style={{ color: palette.accent, textDecoration: "none", cursor: "pointer" }}>{it.headline}</a>
          <div style={{ color: palette.textMuted, marginTop: 2 }}>{it.source} · seen {formatTapeTime(it.seen_at)}</div>
        </div>
      ))}
    </div>
  );
}
