import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { formatTapeTime, formatPrice, QUOTE_DECIMALS } from "../../render/format";
import type { Palette } from "../../render/palette";
import { openNewsWindow } from "../windows";

// Mirrors the engine default; classification stays exclusively in the engine.
const CATALYST_MIN_SCORE = 50;

/** Classifies a news item's effective timestamp as "today" (bronze fresh treatment) vs an older, muted date. */
export function newsDateLabel(seenAtISO: string, nowMs: number): { label: string; today: boolean } {
  const d = new Date(seenAtISO);
  const now = new Date(nowMs);
  const sameDay = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  if (sameDay) return { label: "today", today: true };
  return { label: d.toLocaleDateString("en-US", { month: "short", day: "numeric" }), today: false };
}

function newsMeta(item: { published_at: string; published_precision: string; seen_at: string }, nowMs: number): { label: string; detail: string; today: boolean } {
  if (item.published_precision === "unknown" || !item.published_at) {
    return { label: "time unavailable", detail: `seen ${formatTapeTime(item.seen_at)}`, today: false };
  }
  const date = newsDateLabel(item.published_at, nowMs);
  return { label: date.label, detail: item.published_precision === "date" ? "" : formatTapeTime(item.published_at), today: date.today };
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

function catalystBadge(category: string, palette: Palette): JSX.Element | null {
  const labels: Record<string, string> = { earnings: "EARNINGS", guidance: "GUIDANCE", offering: "OFFERING", financing: "FINANCING", fda_clinical: "FDA", contract: "CONTRACT", merger_acquisition: "M&A", bankruptcy: "BANKRUPTCY", regulatory: "REGULATORY", analyst: "ANALYST", corporate_action: "CORP ACTION", halt: "HALT" };
  const label = labels[category];
  return label ? <span className="mono" style={{ fontSize: 9, border: `1px solid ${palette.accent}`, color: palette.accent, padding: "0 3px", marginRight: 4 }}>[{label}]</span> : null;
}

function textOrDash(value: string, palette: Palette): JSX.Element {
  return value
    ? <span className="mono" style={{ color: palette.text }}>{value}</span>
    : <span className="mono" style={{ color: palette.textMuted }}>—</span>;
}

function borrowStatusLabel(value: string | null | undefined): string | null {
  if (!value?.trim()) return null;
  if (value === "easy_to_borrow") return "ETB";
  if (value === "hard_to_borrow") return "HTB";
  const readable = value.trim().replace(/_/g, " ").toLowerCase();
  return readable.charAt(0).toUpperCase() + readable.slice(1);
}

function nullableBoolean(value: boolean | null | undefined, palette: Palette): JSX.Element {
  const label = value == null ? "—" : value === true ? "Yes" : "No";
  return <span className="mono" style={{ color: value == null ? palette.textMuted : palette.text }}>{label}</span>;
}

export function StockInfoPanel({ config, stores, linkGroups, group: groupProp, symbol: symbolProp, onConfigChange }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const snap = useSyncExternalStore((cb) => stores.news.subscribe(cb), () => stores.news.getSnapshot());
  const detailSnap = useSyncExternalStore((cb) => stores.stockDetail.subscribe(cb), () => stores.stockDetail.getSnapshot());
  // config.group is frozen (dockview never re-invokes this panel's factory with a
  // fresh config after creation); PanelFrame's live `group` prop is what actually
  // changes on a group re-pick — see registry.ts's PanelProps.group comment.
  const group = groupProp ?? config.group;
  const configuredSymbol = typeof config.settings.symbol === "string" ? config.settings.symbol : undefined;
  const [symbol, setSymbol] = useState<string | undefined>(() => symbolProp ?? linkGroups.symbolFor(group) ?? configuredSymbol);
  useEffect(() => {
    const apply = () => setSymbol(symbolProp ?? linkGroups.symbolFor(group) ?? configuredSymbol);
    apply();
    return linkGroups.subscribe(apply);
  }, [linkGroups, group, symbolProp, configuredSymbol]);
  const [catalystsOnly, setCatalystsOnly] = useState<boolean>(() => (config.settings.catalystsOnly as boolean) ?? true);
  // Default collapsed: a compact one-line summary (name · sector, no price/change)
  // so the news list starts higher. Persisted per panel like
  // ChartPanel's timeframe/indicators — patch only this key, never spread config.settings.
  const [detailsCollapsed, setDetailsCollapsed] = useState<boolean>(() => (config.settings.detailsCollapsed as boolean) ?? true);
  const toggleDetails = () => {
    const next = !detailsCollapsed;
    setDetailsCollapsed(next);
    onConfigChange({ detailsCollapsed: next });
  };

  const items = useMemo(() => (symbol ? stores.news.itemsFor(symbol) : []), [snap, symbol, stores.news]);
  // Derived from `items`, never mutates it — something else may reasonably
  // re-derive from the unfiltered list later.
  const visibleItems = useMemo(() => catalystsOnly ? items.filter((it) => it.catalyst_score >= CATALYST_MIN_SCORE && it.catalyst_category !== "other") : items, [items, catalystsOnly]);
  const detail = useMemo(
    () => (symbol ? stores.stockDetail.detailFor(symbol) : undefined),
    [detailSnap, symbol, stores.stockDetail],
  );
  const borrowStatus = borrowStatusLabel(detail?.borrowStatus);
  const hasAlpacaStatus = detail != null && (
    detail.borrowStatus != null || detail.shortable != null || detail.marginable != null || detail.tradable != null
  );
  return (
    <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      {/* Reserved slot for high-salience halt banners (v2 feed) — empty in v1. */}
      <div data-testid="halt-slot" />
      {/* The dockview tab already reads "Stock Info" — this in-body line only
       * fills the empty-state gap when no symbol is focused, so it doesn't
       * repeat the tab title once a symbol (and the name/price row below) is showing. */}
      {!symbol && (
        <div style={{ padding: "6px 8px", color: palette.textMuted }}>No symbol focused</div>
      )}

      {symbol && (
        detail === undefined ? (
          <div style={{ padding: 12, color: palette.textMuted }}>No fundamentals yet for {symbol}.</div>
        ) : (
          <>
            {detailsCollapsed ? (
              <div style={{ padding: "6px 8px", display: "flex", alignItems: "baseline", gap: 6, flexWrap: "wrap" }}>
                <span style={{ fontWeight: 600, color: detail.name ? palette.text : palette.textMuted }}>
                  {detail.name || "—"}
                </span>
                {detail.sector && (
                  <>
                    <span style={{ color: palette.textMuted }}>·</span>
                    {textOrDash(detail.sector, palette)}
                  </>
                )}
                {detail.shortable === false ? (
                  <>
                    <span style={{ color: palette.textMuted }}>·</span>
                    <span className="mono" style={{ color: palette.text }}>Not Shortable</span>
                  </>
                ) : borrowStatus && (
                  <>
                    <span style={{ color: palette.textMuted }}>·</span>
                    <span className="mono" style={{ color: palette.text }}>{borrowStatus}</span>
                  </>
                )}
                {detail.tradable != null && (
                  <>
                    <span style={{ color: palette.textMuted }}>·</span>
                    <span className="mono" style={{ color: palette.text }}>{detail.tradable ? "Tradable" : "NOT Tradeable"}</span>
                  </>
                )}
                <button type="button" onClick={toggleDetails} aria-expanded={false} aria-label="Toggle fundamentals"
                  style={{ marginLeft: "auto", background: "transparent", border: "none", padding: 0, cursor: "pointer", color: palette.textMuted, fontSize: 11 }}>
                  ▸
                </button>
              </div>
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
                  <button type="button" onClick={toggleDetails} aria-expanded={true} aria-label="Toggle fundamentals"
                    style={{ marginLeft: "auto", background: "transparent", border: "none", padding: 0, cursor: "pointer", color: palette.textMuted, fontSize: 11 }}>
                    ▾
                  </button>
                </div>
                <div style={{ borderBottom: `1px solid ${palette.border}` }} />
                <div style={{ display: "grid", gridTemplateColumns: "auto 1fr auto 1fr", gap: "2px 8px", fontSize: 11, padding: "4px 8px" }}>
                  <span style={{ color: palette.textMuted }}>Exchange</span>
                  {textOrDash(detail.exchange, palette)}

                  {detail.country && (
                    <>
                      <span style={{ color: palette.textMuted }}>Country</span>
                      {textOrDash(detail.country, palette)}
                    </>
                  )}

                  {detail.sector && (
                    <>
                      <span style={{ color: palette.textMuted }}>Sector</span>
                      {textOrDash(detail.sector, palette)}
                    </>
                  )}

                  <span style={{ color: palette.textMuted }}>Industry</span>
                  {textOrDash(detail.industry, palette)}

                  {hasAlpacaStatus && (
                    <>
                      <span style={{ color: palette.textMuted }}>Borrow status</span>
                      {textOrDash(borrowStatus ?? "", palette)}
                      <span style={{ color: palette.textMuted }}>Shortable</span>
                      {nullableBoolean(detail.shortable, palette)}
                      <span style={{ color: palette.textMuted }}>Marginable</span>
                      {nullableBoolean(detail.marginable, palette)}
                      <span style={{ color: palette.textMuted }}>Tradable</span>
                      {nullableBoolean(detail.tradable, palette)}
                    </>
                  )}
                </div>
              </>
            )}
          </>
        )
      )}
      {symbol && <div style={{ borderBottom: `1px solid ${palette.borderStrong}` }} />}

      {symbol && (
        <>
          <div style={{ background: palette.surface, display: "flex", alignItems: "center", gap: 8, padding: "4px 8px", borderBottom: `1px solid ${palette.border}` }}>
            <label style={{ display: "flex", alignItems: "center", gap: 4, cursor: "pointer", color: catalystsOnly ? palette.text : palette.textMuted }}>
              <input type="checkbox" checked={catalystsOnly} onChange={(e) => { const next = e.target.checked; setCatalystsOnly(next); onConfigChange({ catalystsOnly: next }); }} style={{ width: 12, height: 12 }} />
              Catalysts only
            </label>
          </div>

          {items.length === 0 && (
            <div style={{ padding: 12, color: palette.textMuted }}>No news for {symbol}.</div>
          )}
          {items.length > 0 && visibleItems.length === 0 && (
            <div style={{ padding: 12, color: palette.textMuted }}>No catalyst news for {symbol}.</div>
          )}
          {visibleItems.map((it) => {
            const meta = newsMeta(it, Date.now());
            return (
              <div key={it.id}
                style={{
                  padding: "6px 8px", borderBottom: `1px solid ${palette.border}`,
                  ...(meta.today ? { background: "rgba(154,106,27,.08)", boxShadow: "inset 2px 0 0 var(--accent)" } : {}),
                }}>
                {typeBadge(it.type, palette)}
                {catalystBadge(it.catalyst_category, palette)}
                <a href={it.url} onClick={(e) => { e.preventDefault(); openNewsWindow(it.url); }}
                  style={{ color: palette.accent, textDecoration: "none", cursor: "pointer" }}>{it.headline}</a>
                <div className="mono" style={{ marginTop: 2 }}>
                  <span style={{ color: meta.today ? palette.accent : palette.textMuted }}>{meta.label}</span>
                  {meta.detail && <span style={{ color: palette.textMuted }}> · {meta.detail}</span>}
                  {it.source && <span style={{ color: palette.textMuted }}> · {it.source}</span>}
                </div>
              </div>
            );
          })}
        </>
      )}
    </div>
  );
}
