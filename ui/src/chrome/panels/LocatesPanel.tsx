import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties } from "react";
import type { PanelProps } from "./registry";
import type { LocateEligibility, LocateListResult, LocateQuote, LocateQuoteResult, LocateRecord } from "../../gen/wsmsg";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useVenueSelection } from "../exec/venueSelection";
import { normalizeSymbol } from "../symbol";
import { getTvChrome } from "../../render/chart/tvTheme";
import { TVDialog } from "./tv/TVDialog";

type Tab = "active" | "history";
type Scope = "all" | "symbol";

const emptyList: LocateRecord[] = [];

function bareSymbol(symbol: string): string {
  return symbol.replace(/^US\./, "");
}

function positiveDecimal(raw: string): boolean {
  return /^(?=.*[1-9])\d+(?:\.\d+)?$/.test(raw.trim());
}

// Keeps fee estimates exact without converting Alpaca's decimal strings to
// binary floating point. This is display-only arithmetic; broker values stay
// strings all the way through the engine.
function multiplyDecimal(qty: number, raw: string): string {
  const value = raw.trim();
  if (!positiveDecimal(value) || !Number.isSafeInteger(qty) || qty <= 0) return "";
  const [whole, fraction = ""] = value.split(".");
  const digits = BigInt(`${whole}${fraction}`) * BigInt(qty);
  let text = digits.toString().padStart(fraction.length + 1, "0");
  if (fraction.length > 0) {
    const point = text.length - fraction.length;
    text = `${text.slice(0, point)}.${text.slice(point)}`;
    text = text.replace(/0+$/, "").replace(/\.$/, "");
  }
  return text;
}

function money(raw: string): string {
  return raw ? `$${raw}` : "—";
}

function displayTime(raw: string): string {
  if (!raw) return "—";
  const date = new Date(raw);
  return Number.isNaN(date.getTime()) ? raw : date.toLocaleString();
}

function boolText(value: boolean | null | undefined): string {
  return value === undefined || value === null ? "—" : value ? "Yes" : "No";
}

function statusChip(status: string, palette: ReturnType<typeof useTheme>["palette"]): JSX.Element {
  const upper = status.toUpperCase() || "UNKNOWN";
  const color = upper === "ACTIVE" ? palette.ok : upper === "REJECTED" ? palette.danger : palette.textMuted;
  return <span style={{ color, border: `1px solid ${color}`, borderRadius: 3, padding: "1px 5px", fontSize: 10 }}>{upper}</span>;
}

export function LocatesPanel({ config, stores, commands, linkGroups, group: groupProp, symbol: symbolProp, onConfigChange }: PanelProps): JSX.Element {
  const { palette, mode } = useTheme();
  const toast = useToasts();
  const group = groupProp === undefined ? config.group : groupProp;
  const { venue, selectVenue } = useVenueSelection(group, linkGroups, stores);
  const status = stores.exec.status();
  const rawSymbol = symbolProp ?? linkGroups.symbolFor(group) ?? (config.settings.symbol as string) ?? "";
  const symbol = rawSymbol ? normalizeSymbol(rawSymbol) : "";
  const identity = `${venue}|${symbol}`;
  const identityRef = useRef(identity);
  identityRef.current = identity;
  const generationRef = useRef(0);
  const activeListSeqRef = useRef(0);
  const historyListSeqRef = useRef(0);
  const activeListIdentityRef = useRef("");
  const historyListIdentityRef = useRef("");
  const inFlightRef = useRef(false);
  const requestSeqRef = useRef(0);
  const requestKeyRef = useRef<{ fingerprint: string; key: string } | null>(null);

  const alpacaVenues = useMemo(
    () => status?.venues.filter((v) => v.broker === "alpaca").map((v) => v.venue) ?? [],
    [status],
  );
  const supported = alpacaVenues.includes(venue);
  const selectedAlpaca = supported;

  const [quantityText, setQuantityText] = useState("100");
  const [allOrNone, setAllOrNone] = useState(() => config.settings.allOrNone !== false);
  const [activeTab, setActiveTab] = useState<Tab>(() => config.settings.tab === "history" ? "history" : "active");
  const [activeScope, setActiveScope] = useState<Scope>(() => config.settings.scope === "symbol" ? "symbol" : "all");
  const [historyStatus, setHistoryStatus] = useState(() => config.settings.historyStatus === "rejected" ? "rejected" : "expired");
  const [eligibility, setEligibility] = useState<LocateEligibility | null>(null);
  const [quote, setQuote] = useState<LocateQuote | null>(null);
  const [maxFee, setMaxFee] = useState("");
  const [quoteLoading, setQuoteLoading] = useState(false);
  const [quoteError, setQuoteError] = useState("");
  const [locateResult, setLocateResult] = useState<LocateRecord | null>(null);
  const [requestError, setRequestError] = useState("");
  const [activeListError, setActiveListError] = useState("");
  const [historyListError, setHistoryListError] = useState("");
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [activeLocates, setActiveLocates] = useState<LocateRecord[]>(emptyList);
  const [historyLocates, setHistoryLocates] = useState<LocateRecord[]>(emptyList);
  const [historyNext, setHistoryNext] = useState("");

  const activeFilterSymbol = activeScope === "symbol" ? symbol : "";
  const activeListIdentity = `${venue}|${activeFilterSymbol}`;
  const historyListIdentity = `${venue}|${historyStatus}`;
  const listError = activeTab === "active" ? activeListError : historyListError;
  activeListIdentityRef.current = activeListIdentity;
  historyListIdentityRef.current = historyListIdentity;

  const quantity = Number(quantityText);
  const quantityValid = Number.isSafeInteger(quantity) && quantity > 0 && quantity % 100 === 0;
  const maxFeeValid = positiveDecimal(maxFee);
  const fingerprint = `${venue}|${symbol}|${quantityText}|${maxFee}|${allOrNone}`;
  const borrowStatus = eligibility?.borrowStatus?.toLowerCase() ?? "";
  const notShortable = eligibility?.shortable === false;
  const easyToBorrow = borrowStatus === "easy_to_borrow";
  const hardToBorrow = borrowStatus === "hard_to_borrow";
  const statusLabel = notShortable ? "NOT SHORTABLE" : easyToBorrow ? "EASY TO BORROW" : hardToBorrow ? "HARD TO BORROW" : "BORROW STATUS UNKNOWN";
  const workflowEnabled = !!symbol && selectedAlpaca && symbol.startsWith("US.") && !easyToBorrow && !notShortable;
  const estimatedFee = quote && quantityValid ? multiplyDecimal(quantity, quote.price) : "";
  const maximumFee = maxFeeValid && quantityValid ? multiplyDecimal(quantity, maxFee) : "";

  useEffect(() => {
    if (requestKeyRef.current && requestKeyRef.current.fingerprint !== fingerprint) requestKeyRef.current = null;
  }, [fingerprint]);

  // Symbol/venue changes invalidate quote and request state. List views use
  // separate identities below so All/History can survive symbol changes.
  useEffect(() => {
    generationRef.current += 1;
    setEligibility(null);
    setQuote(null);
    setMaxFee("");
    setQuoteError("");
    setLocateResult(null);
    setRequestError("");
    setConfirmOpen(false);
    setQuoteLoading(false);
    requestSeqRef.current += 1;
    inFlightRef.current = false;
    requestKeyRef.current = null;
    setSubmitting(false);
  }, [identity]);

  useEffect(() => {
    setActiveLocates([]);
    setActiveListError("");
    activeListSeqRef.current += 1;
  }, [activeListIdentity]);

  useEffect(() => {
    setHistoryLocates([]);
    setHistoryNext("");
    setHistoryListError("");
    historyListSeqRef.current += 1;
  }, [historyListIdentity]);

  useEffect(() => {
    if (!supported || !symbol) return;
    const generation = generationRef.current;
    const requestIdentity = identity;
    void commands.sendQuery("QueryLocateEligibility", { venue, symbol }).then((raw) => {
      if (generation !== generationRef.current || identityRef.current !== requestIdentity) return;
      setEligibility(raw as LocateEligibility);
    }).catch((err: unknown) => {
      if (generation === generationRef.current && identityRef.current === requestIdentity) {
        setEligibility({ supported: true, found: false, borrowStatus: null, shortable: null, marginable: null, tradable: null, error: err instanceof Error ? err.message : "eligibility unavailable" });
      }
    });
  }, [commands, identity, supported, symbol, venue]);

  const loadLocates = useCallback(async (filter: { status: string; symbol: string; pageToken?: string }, target: Tab, append: boolean): Promise<void> => {
    const seqRef = target === "active" ? activeListSeqRef : historyListSeqRef;
    const identityRef = target === "active" ? activeListIdentityRef : historyListIdentityRef;
    const setListError = target === "active" ? setActiveListError : setHistoryListError;
    const seq = ++seqRef.current;
    const requestIdentity = target === "active" ? `${venue}|${filter.symbol}` : `${venue}|${filter.status}`;
    const isCurrent = () => seq === seqRef.current && identityRef.current === requestIdentity;
    try {
      const raw = await commands.sendQuery("QueryLocates", {
        venue, status: filter.status, symbol: filter.symbol, start: "", end: "", limit: 100, pageToken: filter.pageToken ?? "",
      }) as LocateListResult;
      if (!isCurrent()) return;
      if (raw.error) {
        setListError(raw.error);
        return;
      }
      setListError("");
      if (target === "active") {
        setActiveLocates((current) => append ? [...current, ...(raw.locates ?? [])] : (raw.locates ?? []));
      } else {
        setHistoryLocates((current) => append ? [...current, ...(raw.locates ?? [])] : (raw.locates ?? []));
        setHistoryNext(raw.nextPageToken ?? "");
      }
    } catch (err: unknown) {
      if (isCurrent()) setListError(err instanceof Error ? err.message : "locates unavailable");
    }
  }, [commands, venue]);

  useEffect(() => {
    if (!supported) return;
    void loadLocates({ status: "active", symbol: activeFilterSymbol }, "active", false);
  }, [activeFilterSymbol, activeListIdentity, loadLocates, supported]);

  useEffect(() => {
    if (!supported || activeTab !== "history") return;
    void loadLocates({ status: historyStatus, symbol: "" }, "history", false);
  }, [activeTab, historyListIdentity, historyStatus, loadLocates, supported]);

  const getQuote = async (): Promise<void> => {
    if (!workflowEnabled || !quantityValid || quoteLoading) return;
    const requestIdentity = identity;
    const generation = generationRef.current;
    setQuoteLoading(true);
    setQuoteError("");
    setRequestError("");
    setLocateResult(null);
    try {
      const raw = await commands.sendQuery("QueryLocateQuotes", { venue, symbols: [symbol] }) as LocateQuoteResult;
      if (generation !== generationRef.current || identityRef.current !== requestIdentity) return;
      const next = raw.quotes?.find((item) => normalizeSymbol(item.symbol) === symbol) ?? null;
      setQuote(next);
      setMaxFee(next?.price ?? "");
      if (raw.error) setQuoteError(raw.error);
      else if (!next) setQuoteError(raw.errors?.length ? raw.errors.map((item) => item.message || item.code || item.symbol).join("; ") : "quote unavailable");
    } catch (err: unknown) {
      if (generation === generationRef.current && identityRef.current === requestIdentity) setQuoteError(err instanceof Error ? err.message : "locate quote unavailable");
    } finally {
      if (generation === generationRef.current && identityRef.current === requestIdentity) setQuoteLoading(false);
    }
  };

  const openConfirmation = () => {
    if (!quote || !quantityValid || !maxFeeValid || submitting) return;
    setRequestError("");
    setConfirmOpen(true);
  };

  const requestLocate = async (): Promise<void> => {
    if (inFlightRef.current || !quote || !quantityValid || !maxFeeValid) return;
    const requestIdentity = identity;
    const generation = generationRef.current;
    const requestSeq = requestSeqRef.current + 1;
    requestSeqRef.current = requestSeq;
    inFlightRef.current = true;
    setSubmitting(true);
    setRequestError("");
    const current = requestKeyRef.current?.fingerprint === fingerprint ? requestKeyRef.current.key : (globalThis.crypto?.randomUUID?.() ?? `locate-${Date.now()}-${Math.random().toString(36).slice(2)}`);
    requestKeyRef.current = { fingerprint, key: current };
    try {
      const ack = await commands.sendCommand("RequestLocate", {
        venue, symbol, qty: quantity, limitPrice: maxFee, allOrNone, idempotencyKey: current,
      });
      if (requestSeq !== requestSeqRef.current || generation !== generationRef.current || identityRef.current !== requestIdentity) return;
      if (ack.status !== "accepted") {
        if (!ack.ambiguous) requestKeyRef.current = null;
        setRequestError(ack.reason ?? "locate request rejected");
        setConfirmOpen(false);
        return;
      }
      const record = ack.value as LocateRecord | undefined;
      if (!record || typeof record !== "object" || typeof record.id !== "string") {
        setRequestError("locate response did not include a record");
        setConfirmOpen(false);
        return;
      }
      setLocateResult(record);
      setQuote(null);
      requestKeyRef.current = null;
      setConfirmOpen(false);
      toast.push({ level: "success", text: "Locate active. Short order may now be submitted." });
      void loadLocates({ status: "active", symbol: activeFilterSymbol }, "active", false);
    } catch (err: unknown) {
      if (requestSeq === requestSeqRef.current && generation === generationRef.current && identityRef.current === requestIdentity) {
        setRequestError(err instanceof Error ? err.message : "locate request failed");
        setConfirmOpen(false);
      }
    } finally {
      if (requestSeq === requestSeqRef.current) {
        inFlightRef.current = false;
        setSubmitting(false);
      }
    }
  };

  const setScope = (next: Scope) => { setActiveScope(next); onConfigChange({ scope: next }); };
  const setTab = (next: Tab) => { setActiveTab(next); onConfigChange({ tab: next }); };
  const setAon = (next: boolean) => { setAllOrNone(next); onConfigChange({ allOrNone: next }); };
  const updateHistoryStatus = (next: string) => { setHistoryStatus(next); onConfigChange({ historyStatus: next }); };
  const selectorValue = selectedAlpaca ? venue : "";

  return (
    <div data-testid="locates-panel" style={{ height: "100%", minWidth: 0, overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 8px", borderBottom: `1px solid ${palette.border}`, background: palette.surface }}>
        <span style={{ color: palette.textMuted }}>Venue</span>
        <select data-testid="locates-venue" className="ctl mono" value={selectorValue} onChange={(e) => selectVenue(e.target.value)}>
          {!selectedAlpaca && venue && <option value="" disabled>{venue}</option>}
          {alpacaVenues.map((item) => <option key={item} value={item}>{item}</option>)}
        </select>
      </div>

      {status === null ? <div style={{ padding: 12, color: palette.textMuted }}>Loading venue status…</div>
        : !symbol ? <div data-testid="locates-unassigned" style={{ padding: 12, color: palette.textMuted }}>No symbol focused — type or link a symbol.</div>
        : alpacaVenues.length === 0 ? <div data-testid="locates-empty" style={{ padding: 12, color: palette.textMuted }}>No Alpaca venue configured.</div>
          : !selectedAlpaca ? <div data-testid="locates-unsupported" style={{ padding: 12 }}>
            <strong>LOCATES NOT AVAILABLE FOR THIS VENUE</strong>
            <div style={{ color: palette.textMuted, marginTop: 6 }}>Selected venue: {venue || "none"}</div>
            <div style={{ color: palette.textMuted, marginTop: 4 }}>Select an Alpaca venue to request locates.</div>
          </div>
            : <>
              <section style={{ padding: 8, borderBottom: `1px solid ${palette.border}` }}>
                <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline" }}>
                  <strong data-testid="locates-borrow-status" style={{ color: notShortable ? palette.danger : hardToBorrow ? palette.warn : easyToBorrow ? palette.ok : palette.textMuted }}>{statusLabel}</strong>
                  <span style={{ color: eligibility?.shortable === false ? palette.danger : palette.ok }}>{eligibility ? (eligibility.shortable === null ? "SHORTABLE UNKNOWN" : eligibility.shortable ? "SHORTABLE" : "NOT SHORTABLE") : ""}</span>
                </div>
                <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 4, marginTop: 6, color: palette.textMuted }}>
                  <span>Marginable: <b style={{ color: palette.text }}>{boolText(eligibility?.marginable)}</b></span>
                  <span>Tradable: <b style={{ color: palette.text }}>{boolText(eligibility?.tradable)}</b></span>
                </div>
                {eligibility?.error && <div style={{ color: palette.warn, marginTop: 5 }}>{eligibility.error}</div>}
              </section>

              <section style={{ padding: 8, borderBottom: `1px solid ${palette.border}` }}>
                <div style={{ color: palette.textMuted, marginBottom: 3 }}>Quantity</div>
                <div style={{ display: "flex", alignItems: "center", gap: 5 }}>
                  <button type="button" className="ctl mono" onClick={() => setQuantityText(String(Math.max(100, (Number(quantityText) || 100) - 100)))} disabled={!workflowEnabled}>−100</button>
                  <input data-testid="locates-quantity" className="ctl mono" type="number" min={100} step={100} value={quantityText} onChange={(e) => setQuantityText(e.target.value)} disabled={!workflowEnabled} style={{ flex: 1, minWidth: 0 }} />
                  <button type="button" className="ctl mono" onClick={() => setQuantityText(String((Number(quantityText) || 0) + 100))} disabled={!workflowEnabled}>+100</button>
                </div>
                {!quantityValid && <div data-testid="locates-quantity-error" style={{ color: palette.danger, marginTop: 4 }}>Quantity must be a multiple of 100 shares.</div>}
                <label style={{ display: "flex", gap: 6, alignItems: "center", marginTop: 8, color: palette.textMuted }}>
                  <input data-testid="locates-all-or-none" type="checkbox" checked={allOrNone} onChange={(e) => setAon(e.target.checked)} disabled={!workflowEnabled} />
                  <span>All or none <small>({allOrNone ? "require the full requested quantity" : "partial locate may be accepted"})</small></span>
                </label>
                <button data-testid="get-locate-quote" type="button" className="ctl" onClick={() => void getQuote()} disabled={!workflowEnabled || !quantityValid || quoteLoading} style={{ width: "100%", marginTop: 10, fontWeight: 700 }}>
                  {quoteLoading ? "GETTING QUOTE…" : "GET LOCATE QUOTE"}
                </button>
                {easyToBorrow && <div style={{ color: palette.ok, marginTop: 8 }}>No locate required. Submit a normal short order.</div>}
                {notShortable && <div style={{ color: palette.danger, marginTop: 8 }}>This asset is not shortable.</div>}
              </section>

              {quote && !locateResult && <section data-testid="locate-quote" style={{ padding: 8, borderBottom: `1px solid ${palette.border}` }}>
                <div style={{ display: "flex", justifyContent: "space-between" }}><strong>LOCATE QUOTE</strong><span style={{ color: palette.textMuted }}>{displayTime(quote.quotedAt)}</span></div>
                <div style={{ display: "grid", gridTemplateColumns: "1fr auto", gap: 5, marginTop: 8 }}>
                  <span style={{ color: palette.textMuted }}>Available</span><b className="mono">{quote.availableQty.toLocaleString()}</b>
                  <span style={{ color: palette.textMuted }}>Locate fee/share</span><b className="mono">{money(quote.price)}</b>
                  <span style={{ color: palette.textMuted }}>Requested quantity</span><b className="mono">{quantityValid ? quantity.toLocaleString() : "—"}</b>
                  <span style={{ color: palette.textMuted }}>Estimated locate fee</span><b className="mono">{money(estimatedFee)}</b>
                </div>
                <label style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8, marginTop: 10 }}>
                  <span style={{ color: palette.textMuted }}>Maximum fee/share</span>
                  <input data-testid="locates-max-fee" className="ctl mono" value={maxFee} onChange={(e) => setMaxFee(e.target.value)} style={{ width: 110 }} />
                </label>
                <div style={{ display: "flex", justifyContent: "space-between", marginTop: 5 }}><span style={{ color: palette.textMuted }}>Maximum total fee</span><b className="mono">{money(maximumFee)}</b></div>
                {!maxFeeValid && <div style={{ color: palette.danger, marginTop: 5 }}>Maximum fee/share must be a positive decimal.</div>}
                <button data-testid="request-locate" type="button" className="ctl" onClick={openConfirmation} disabled={!quantityValid || !maxFeeValid || submitting} style={{ width: "100%", marginTop: 10, fontWeight: 700 }}>
                  REQUEST LOCATE{maximumFee ? ` · MAX ${money(maximumFee)}` : ""}
                </button>
                <div style={{ color: palette.textMuted, marginTop: 7, fontSize: 11 }}>Locate fees are non-refundable and separate from HTB stock borrow fees.</div>
              </section>}

              {locateResult && <section data-testid="locate-success" style={{ padding: 8, borderBottom: `1px solid ${palette.border}` }}>
                <strong style={{ color: palette.ok }}>✓ LOCATE ACTIVE</strong>
                <div className="mono" style={{ marginTop: 6 }}>{bareSymbol(locatesSymbol(locateResult, symbol))}</div>
                <div style={{ display: "grid", gridTemplateColumns: "1fr auto", gap: 5, marginTop: 8 }}>
                  <span style={{ color: palette.textMuted }}>Requested</span><b className="mono">{locateResult.requestedQty.toLocaleString()}</b>
                  <span style={{ color: palette.textMuted }}>Located</span><b className="mono">{locateResult.locatedQty.toLocaleString()}</b>
                  <span style={{ color: palette.textMuted }}>Fee/share</span><b className="mono">{money(locateResult.locatedPrice)}</b>
                  <span style={{ color: palette.textMuted }}>Total fee</span><b className="mono">{money(locateResult.totalFee)}</b>
                  <span style={{ color: palette.textMuted }}>Expires</span><b className="mono">{displayTime(locateResult.expiresAt)}</b>
                </div>
                <div style={{ color: palette.ok, marginTop: 8 }}>Short order may now be submitted.</div>
              </section>}

              {(quoteError || requestError || listError) && <div data-testid="locates-error" style={{ padding: "6px 8px", color: palette.danger, borderBottom: `1px solid ${palette.border}` }}>{quoteError || requestError || listError}</div>}

              <section style={{ display: "flex", flexDirection: "column", minHeight: 130 }}>
                <div style={{ display: "flex", alignItems: "center", borderBottom: `1px solid ${palette.border}`, background: palette.surface }}>
                  <button type="button" onClick={() => setTab("active")} style={tabStyle(activeTab === "active", palette)}>ACTIVE</button>
                  <button type="button" onClick={() => setTab("history")} style={tabStyle(activeTab === "history", palette)}>HISTORY</button>
                  <div style={{ flex: 1 }} />
                  {activeTab === "active" ? <>
                    <button type="button" onClick={() => setScope("all")} style={tabStyle(activeScope === "all", palette)}>All</button>
                    <button type="button" onClick={() => setScope("symbol")} style={tabStyle(activeScope === "symbol", palette)}>This Symbol</button>
                    <button type="button" aria-label="refresh active locates" onClick={() => void loadLocates({ status: "active", symbol: activeFilterSymbol }, "active", false)} style={refreshStyle(palette)}>↻</button>
                  </> : <>
                    <select data-testid="locates-history-status" className="ctl mono" value={historyStatus} onChange={(e) => updateHistoryStatus(e.target.value)} style={{ marginRight: 6 }}>
                      <option value="expired">Expired</option><option value="rejected">Rejected</option>
                    </select>
                    {historyNext && <button type="button" onClick={() => void loadLocates({ status: historyStatus, symbol: "", pageToken: historyNext }, "history", true)} style={refreshStyle(palette)}>Load more</button>}
                  </>}
                </div>
                <div style={{ overflow: "auto", minHeight: 0, flex: 1 }}>
                  {(activeTab === "active" ? activeLocates : historyLocates).length === 0 ? <div style={{ padding: 10, color: palette.textMuted }}>No locates.</div> : <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 11 }}>
                    <thead><tr style={{ color: palette.textMuted, textAlign: "right" }}><th style={cellStyle}>Symbol</th><th style={cellStyle}>Qty</th><th style={cellStyle}>Fee/sh</th><th style={cellStyle}>Total</th><th style={cellStyle}>Expires</th><th style={cellStyle}>Status</th></tr></thead>
                    <tbody>{(activeTab === "active" ? activeLocates : historyLocates).map((item) => <tr key={item.id} style={{ borderTop: `1px solid ${palette.border}`, textAlign: "right" }}>
                      <td style={{ ...cellStyle, textAlign: "left" }}>{bareSymbol(item.symbol)}</td><td style={cellStyle}>{item.locatedQty || item.requestedQty}</td><td style={cellStyle}>{money(item.locatedPrice)}</td><td style={cellStyle}>{money(item.totalFee)}</td><td style={cellStyle}>{displayTime(item.expiresAt)}</td><td style={cellStyle}>{statusChip(item.status, palette)}</td>
                    </tr>)}</tbody>
                  </table>}
                </div>
              </section>
            </>}

      {confirmOpen && quote && <TVDialog title="Request Locate" chrome={getTvChrome(mode)} onClose={() => { if (!submitting) setConfirmOpen(false); }} width={380}
        footer={{ onOk: () => { if (!submitting) void requestLocate(); }, okLabel: submitting ? "REQUESTING…" : "REQUEST LOCATE" }}>
        <div className="mono" style={{ fontSize: 14, marginBottom: 10 }}>{bareSymbol(symbol)}</div>
        <div style={{ display: "grid", gridTemplateColumns: "1fr auto", gap: 6 }}>
          <span>Shares</span><b>{quantity.toLocaleString()}</b>
          <span>Quoted fee/share</span><b>{money(quote.price)}</b>
          <span>Maximum fee/share</span><b>{money(maxFee)}</b>
          <span>Maximum total fee</span><b>{money(maximumFee)}</b>
          <span>All or none</span><b>{allOrNone ? "Yes" : "No"}</b>
        </div>
        <p style={{ color: palette.warn, marginBottom: 5 }}>Locate fees are non-refundable.</p>
        <p style={{ color: palette.textMuted, margin: 0 }}>This reserves borrow availability only. It does not submit a short order.</p>
      </TVDialog>}
    </div>
  );
}

function locatesSymbol(record: LocateRecord, fallback: string): string {
  return record.symbol || fallback;
}

const cellStyle = { padding: "4px 6px", whiteSpace: "nowrap" } as const;

function tabStyle(active: boolean, palette: ReturnType<typeof useTheme>["palette"]): CSSProperties {
  return { fontSize: 11, padding: "5px 7px", border: "none", borderBottom: `2px solid ${active ? palette.accent : "transparent"}`, background: "transparent", color: active ? palette.text : palette.textMuted, cursor: "pointer" };
}

function refreshStyle(palette: ReturnType<typeof useTheme>["palette"]): CSSProperties {
  return { border: "none", background: "transparent", color: palette.textMuted, cursor: "pointer", padding: "4px 6px" };
}
