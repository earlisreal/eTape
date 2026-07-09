import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { formatTapeTime, formatPrice, QUOTE_DECIMALS } from "../../render/format";
import { formatCompactShares } from "../format";
import type { Palette } from "../../render/palette";

// Tunable: view-count floor for the "Hot only" news filter. A UI-only heuristic
// to declutter the list — not an engine contract; the engine's view_count field
// is purely descriptive, this threshold is local/cosmetic.
const HOT_MIN_VIEWS = 1000;

/** Classifies a news item's effective timestamp as "today" (bronze fresh treatment) vs an older, muted date. */
export function newsDateLabel(seenAtISO: string, nowMs: number): { label: string; today: boolean } {
  const d = new Date(seenAtISO);
  const now = new Date(nowMs);
  const sameDay = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  if (sameDay) return { label: "today", today: true };
  return { label: d.toLocaleDateString("en-US", { month: "short", day: "numeric" }), today: false };
}

/** Bracket-style mono news-type tag — the ledger/tape vocabulary stand-in for a colored pill badge.
 * Falls back to "news" styling for any unrecognized type, matching the engine's own defensive default. */
function typeBadge(type: string, palette: Palette): JSX.Element {
  const kind = type === "notice" || type === "rating" ? type : "news";
  const cfg = kind === "notice"
    ? { label: "[NOTICE]", border: palette.border, color: palette.text }
    : kind === "rating"
    ? { label: "[RATING]", border: palette.accent, color: palette.accent } // the one spot of bronze in the news list — a rating is opinion, not fact
    : { label: "[NEWS]", border: palette.border, color: palette.textMuted };
  return (
    <span className="mono" style={{ fontSize: 9, border: `1px solid ${cfg.border}`, color: cfg.color, padding: "0 3px", marginRight: 4 }}>
      {cfg.label}
    </span>
  );
}

function fmtCompactOrDash(value: number | null, palette: Palette): JSX.Element {
  return value == null
    ? <span className="mono" style={{ color: palette.textMuted }}>—</span>
    : <span className="mono" style={{ color: palette.text }}>{formatCompactShares(value)}</span>;
}

function fmtDecimalOrDash(value: number | null, palette: Palette): JSX.Element {
  return value == null
    ? <span className="mono" style={{ color: palette.textMuted }}>—</span>
    : <span className="mono" style={{ color: palette.text }}>{formatPrice(value, QUOTE_DECIMALS)}</span>;
}

function textOrDash(value: string, palette: Palette): JSX.Element {
  return value
    ? <span className="mono" style={{ color: palette.text }}>{value}</span>
    : <span className="mono" style={{ color: palette.textMuted }}>—</span>;
}

// P/E is a unitless ratio, not a price — 2 decimals (not QUOTE_DECIMALS' 3) reads
// less oddly dense (20.00, not 20.000).
const PE_DECIMALS = 2;

/** Combined "P/E · TTM" cell — each side dashes independently if null, so a missing TTM
 * figure doesn't blank out a known trailing P/E (or vice versa). */
function peCell(pe: number | null, peTTM: number | null, palette: Palette): JSX.Element {
  return (
    <span className="mono">
      {pe == null ? <span style={{ color: palette.textMuted }}>—</span> : <span style={{ color: palette.text }}>{formatPrice(pe, PE_DECIMALS)}</span>}
      <span style={{ color: palette.textMuted }}> · </span>
      {peTTM == null ? <span style={{ color: palette.textMuted }}>—</span> : <span style={{ color: palette.text }}>{formatPrice(peTTM, PE_DECIMALS)}</span>}
    </span>
  );
}

/** Combined "52wk low–high" cell, same independent-dash treatment as peCell. */
function rangeCell(low: number | null, high: number | null, palette: Palette): JSX.Element {
  return (
    <span className="mono">
      {low == null ? <span style={{ color: palette.textMuted }}>—</span> : <span style={{ color: palette.text }}>{formatPrice(low, QUOTE_DECIMALS)}</span>}
      <span style={{ color: palette.textMuted }}>–</span>
      {high == null ? <span style={{ color: palette.textMuted }}>—</span> : <span style={{ color: palette.text }}>{formatPrice(high, QUOTE_DECIMALS)}</span>}
    </span>
  );
}

export function StockInfoPanel({ config, stores, linkGroups, group: groupProp }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const snap = useSyncExternalStore((cb) => stores.news.subscribe(cb), () => stores.news.getSnapshot());
  const detailSnap = useSyncExternalStore((cb) => stores.stockDetail.subscribe(cb), () => stores.stockDetail.getSnapshot());
  // config.group is frozen (dockview never re-invokes this panel's factory with a
  // fresh config after creation); PanelFrame's live `group` prop is what actually
  // changes on a group re-pick — see registry.ts's PanelProps.group comment.
  const group = groupProp ?? config.group;
  const [symbol, setSymbol] = useState<string | undefined>(() => linkGroups.symbolFor(group));
  useEffect(() => {
    setSymbol(linkGroups.symbolFor(group));
    return linkGroups.subscribe(() => setSymbol(linkGroups.symbolFor(group)));
  }, [linkGroups, group]);
  const [hotOnly, setHotOnly] = useState(false);

  const items = useMemo(() => (symbol ? stores.news.itemsFor(symbol) : []), [snap, symbol, stores.news]);
  // Derived from `items`, never mutates it — something else may reasonably
  // re-derive from the unfiltered list later.
  const visibleItems = useMemo(
    () => (hotOnly ? items.filter((it) => it.type === "news" && it.view_count >= HOT_MIN_VIEWS) : items),
    [items, hotOnly],
  );
  const detail = useMemo(
    () => (symbol ? stores.stockDetail.detailFor(symbol) : undefined),
    [detailSnap, symbol, stores.stockDetail],
  );

  return (
    <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      {/* Reserved slot for high-salience halt banners (v2 feed) — empty in v1. */}
      <div data-testid="halt-slot" />
      <div style={{ padding: "6px 8px", fontWeight: 600, borderBottom: `1px solid ${palette.border}` }}>
        {symbol ? `Stock Info · ${symbol}` : "Stock Info · no symbol focused"}
      </div>

      {symbol && (
        detail === undefined ? (
          <div style={{ padding: 12, color: palette.textMuted }}>No fundamentals yet for {symbol}.</div>
        ) : (
          <>
            <div style={{ padding: "6px 8px", display: "flex", alignItems: "baseline", gap: 8, flexWrap: "wrap" }}>
              <span style={{ fontWeight: 600, color: detail.name ? palette.text : palette.textMuted }}>
                {detail.name || "—"}
              </span>
              {detail.price == null ? (
                <span className="mono" style={{ color: palette.textMuted }}>—</span>
              ) : (
                <span className="mono">{formatPrice(detail.price, QUOTE_DECIMALS)}</span>
              )}
              {detail.changePct == null ? (
                <span className="mono" style={{ color: palette.textMuted }}>—</span>
              ) : detail.changePct === 0 ? (
                <span className="mono" style={{ color: palette.textMuted }}>{detail.changePct.toFixed(2)}%</span>
              ) : (
                <span className="mono" style={{ color: detail.changePct > 0 ? palette.ok : palette.danger }}>
                  {detail.changePct > 0 ? "▲" : "▼"} {Math.abs(detail.changePct).toFixed(2)}%
                </span>
              )}
            </div>
            <div style={{ borderBottom: `1px solid ${palette.border}` }} />
            <div style={{ display: "grid", gridTemplateColumns: "auto 1fr auto 1fr", gap: "2px 8px", fontSize: 11, padding: "4px 8px" }}>
              <span style={{ color: palette.textMuted }}>Mkt cap</span>
              {fmtCompactOrDash(detail.marketCap, palette)}
              <span style={{ color: palette.textMuted }}>Float cap</span>
              {fmtCompactOrDash(detail.floatMarketCap, palette)}

              <span style={{ color: palette.textMuted }}>Shares out</span>
              {fmtCompactOrDash(detail.sharesOutstanding, palette)}
              <span style={{ color: palette.textMuted }}>Float</span>
              {fmtCompactOrDash(detail.floatShares, palette)}

              <span style={{ color: palette.textMuted }}>Industry</span>
              {textOrDash(detail.industry, palette)}
              <span style={{ color: palette.textMuted }}>P/E · TTM</span>
              {peCell(detail.pe, detail.peTTM, palette)}

              <span style={{ color: palette.textMuted }}>EPS</span>
              {fmtDecimalOrDash(detail.eps, palette)}
              <span style={{ color: palette.textMuted }}>52wk</span>
              {rangeCell(detail.low52, detail.high52, palette)}

              <span style={{ color: palette.textMuted }}>Volume</span>
              {fmtCompactOrDash(detail.volume, palette)}
            </div>
          </>
        )
      )}
      {symbol && <div style={{ borderBottom: `1px solid ${palette.borderStrong}` }} />}

      {symbol && (
        <>
          <div style={{ background: palette.surface, display: "flex", alignItems: "center", gap: 8, padding: "4px 8px", borderBottom: `1px solid ${palette.border}` }}>
            <label style={{ display: "flex", alignItems: "center", gap: 4, cursor: "pointer", color: hotOnly ? palette.text : palette.textMuted }}>
              <input type="checkbox" checked={hotOnly} onChange={(e) => setHotOnly(e.target.checked)} style={{ width: 12, height: 12 }} />
              Hot only
            </label>
          </div>

          {items.length === 0 && (
            <div style={{ padding: 12, color: palette.textMuted }}>No news for {symbol}.</div>
          )}
          {visibleItems.map((it, i) => {
            const effectiveTs = it.published_at || it.seen_at;
            const { label, today } = newsDateLabel(effectiveTs, Date.now());
            return (
              <div key={it.url || `${it.headline}-${i}`}
                style={{
                  padding: "6px 8px", borderBottom: `1px solid ${palette.border}`,
                  ...(today ? { background: "rgba(154,106,27,.08)", boxShadow: "inset 2px 0 0 var(--accent)" } : {}),
                }}>
                {typeBadge(it.type, palette)}
                <a href={it.url} onClick={(e) => { e.preventDefault(); window.open(it.url, "_blank", "noopener,noreferrer"); }}
                  style={{ color: palette.accent, textDecoration: "none", cursor: "pointer" }}>{it.headline}</a>
                <div className="mono" style={{ marginTop: 2 }}>
                  <span style={{ color: today ? palette.accent : palette.textMuted }}>{label}</span>
                  <span style={{ color: palette.textMuted }}> · {formatTapeTime(effectiveTs)} · {it.source}</span>
                </div>
              </div>
            );
          })}
        </>
      )}
    </div>
  );
}
