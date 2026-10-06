import { useEffect, useRef, type MutableRefObject } from "react";
import type { AckMsg, StopLimitRoutePreview, SubmitRiskEntryArgs } from "../../../wire/contract";
import type { Stores } from "../../../data/registry";
import type { ChartApiFacade } from "../../../render/chart/ChartApiFacade";
import { snapOrderMarkerPrice } from "../../../render/chart/orderMarkers";
import type { Tool } from "../../../render/chart/drawings/interaction";
import { CHART_RISK_ENTRY_EVENT, type OrderConfig, type RiskEntryTemplate } from "../../exec/actionTemplate";
import { riskEntrySize } from "../../exec/riskEntry";
import type { LinkGroup, LinkGroups } from "../../linkGroups";
import { modalTracker } from "../../modalTracker";
interface Props {
    panelId: string;
    active: boolean;
    group: LinkGroup;
    symbol: string;
    contextKey: string;
    hostRef: MutableRefObject<HTMLDivElement | null>;
    facadeRef: MutableRefObject<ChartApiFacade | null>;
    stores: Stores;
    linkGroups: LinkGroups;
    config: OrderConfig;
    configLoaded: boolean;
    activeTool: Tool;
    chooserOpenRef: MutableRefObject<boolean>;
    sendCommand(name: string, args: unknown): Promise<AckMsg>;
    sendQuery(name: string, args: unknown): Promise<unknown>;
}
type Draft = {
    template: RiskEntryTemplate;
    buy?: number;
    sell?: number;
    complete: boolean;
    busy: boolean;
    unknown: boolean;
    maxQty: number;
    route?: StopLimitRoutePreview;
    error?: string;
};
export function ChartRiskEntry(props: Props): JSX.Element {
    const rootRef = useRef<HTMLDivElement | null>(null);
    const latest = useRef(props);
    latest.current = props;
    const venue = props.linkGroups.venueFor(props.group) ?? "";
    const hasRisk = props.config.templates.some(t => t.kind === "risk");
    useEffect(() => {
        if (!hasRisk || !venue)
            return;
        const panelId = `risk-${props.panelId}`;
        void props.sendCommand("SetAccountDemand", { panelId, venue });
        return () => { void props.sendCommand("SetAccountDemand", { panelId, venue: "" }); };
    }, [props.sendCommand, props.panelId, venue, hasRisk]);
    useEffect(() => {
        const host = props.hostRef.current;
        if (!host)
            return;
        let draft: Draft | null = null;
        let pointer: {
            id: number;
            x: number;
            y: number;
            endpoint: "buy" | "sell";
            moved: boolean;
            second: boolean;
        } | null = null;
        let frame: number | null = null;
        const message = (text: string) => { const el = rootRef.current?.querySelector<HTMLElement>("[data-risk-detail]"); if (el)
            el.textContent = text; };
        const clear = () => { draft = null; pointer = null; latest.current.facadeRef.current?.setPanZoomEnabled?.(true); if (rootRef.current)
            rootRef.current.style.display = "none"; };
        const editable = () => !!document.activeElement?.closest("input,textarea,select,[contenteditable='true']");
        const size = () => {
            const a = latest.current.stores.exec.accounts().find(a => a.venue === venue);
            return draft && riskEntrySize(draft.template, draft.buy ?? 0, draft.sell ?? 0, a?.availableCash ?? 0, a?.buyingPower ?? 0);
        };
        const invalid = () => {
            const p = latest.current, status = p.stores.exec.status(), v = status?.venues.find(v => v.venue === venue);
            const a = p.stores.exec.accounts().find(a => a.venue === venue);
            if (!p.active || !document.hasFocus() || modalTracker.isOpen() || editable())
                return "Focus the chart to continue.";
            if (!status?.masterArmed)
                return "Trading is locked. Arm the engine first.";
            if (!v?.connected || v.reconcilePending || v.flattenPending || !v.positionDataReady)
                return "Venue or position data unavailable; reconcile first.";
            if (v.env === "live" && !v.heldStopLimitAcknowledged)
                return "Review / enable live accounts in Settings → Orders & hotkeys.";
            if (!a || a.tsMs <= 0 || Date.now() - a.tsMs > 30000 || a.tsMs > Date.now() + 1000)
                return "Fresh account data required.";
            if (p.stores.exec.positions().some(pos => pos.venue === venue && pos.symbol === p.symbol && pos.qty !== 0))
                return "Risk entry requires a flat symbol.";
            if (p.stores.exec.workingOrdersFor(p.symbol, venue).length)
                return "Risk entry requires no other working orders.";
            if (!draft?.route)
                return draft?.error ?? "Checking engine trigger source…";
            if (draft.route.reason)
                return draft.route.reason;
            if (!draft.route.hasTrustedEligiblePrint || !draft.route.lastEligibleTsMs || Date.now() - draft.route.lastEligibleTsMs > 2000)
                return "Fresh eligible market data required.";
            if (!Number.isFinite(draft.template.value) || draft.template.value <= 0 || (draft.template.mode !== "Dollar" && draft.template.value > 100))
                return "Positive risk value required (percentages at most 100).";
            if (draft.buy !== undefined && draft.sell !== undefined && draft.sell >= draft.buy)
                return "Sell trigger must be below buy trigger.";
            if (draft.complete && !size()?.qty)
                return "Risk or funding budget rounds to zero shares.";
            return "";
        };
        const paint = () => {
            if (!draft || !rootRef.current)
                return;
            rootRef.current.style.display = "block";
            const facade = latest.current.facadeRef.current;
            for (const endpoint of ["buy", "sell"] as const) {
                const line = rootRef.current.querySelector<HTMLElement>(`[data-risk-${endpoint}]`);
                const price = draft[endpoint], y = price === undefined ? null : facade?.priceToCoordinate(price);
                if (line) {
                    line.style.display = y == null ? "none" : "block";
                    line.style.top = `${y}px`;
                    line.style.right = `${facade?.priceScaleWidth() ?? 60}px`;
                    line.textContent = `${endpoint === "buy" ? "BUY" : "SELL"} trigger ${price?.toFixed(price < 1 ? 4 : 2)}`;
                }
            }
            const s = size(), error = invalid();
            const deadline = draft.route?.deadlineMs ? new Intl.DateTimeFormat("en-US", { timeZone: "America/New_York", hour: "numeric", minute: "2-digit", timeZoneName: "short" }).format(draft.route.deadlineMs) : "checking";
            message(draft.unknown ? "Submit outcome unknown. Verify Orders; this setup cannot be resent." : draft.busy ? "Submitting linked pair…" :
                `${draft.template.label} · ${venue} · ${latest.current.symbol}\n${draft.buy === undefined ? "Click BUY trigger, then SELL stop; or drag between them." : draft.sell === undefined ? "Choose SELL stop below BUY." :
                    `${s?.qty} shares · buy limit ${s?.buyLimit.toFixed(4)} · sell limit ${s?.sellLimit.toFixed(4)} · notional $${s?.notional.toFixed(2)} · estimated risk $${s?.risk.toFixed(2)}`}\nHeld by eTape · DAY through ${deadline} · fees / execution risk excluded${draft.route?.lastEligiblePrice && draft.buy !== undefined && draft.route.lastEligiblePrice >= draft.buy ? " · BUY WILL TRIGGER NOW" : ""}\n${error || (draft.complete ? "Drag either line to adjust. Enter / Send to submit; Escape cancels." : "Escape cancels.")}${draft.error ? `\n${draft.error}` : ""}`);
        };
        const schedule = () => { if (frame === null)
            frame = requestAnimationFrame(() => { frame = null; paint(); }); };
        const submit = async () => {
            if (!draft?.complete || draft.busy || draft.unknown || draft.buy === undefined || draft.sell === undefined)
                return;
            const current = draft;
            // Re-query at submit; a stale preview never authorizes a delayed send.
            current.busy = true;
            paint();
            try {
                current.route = await latest.current.sendQuery("QueryStopLimitRoute", { tif: "DAY", session: "EXTENDED", symbol: latest.current.symbol, riskEntry: true }) as StopLimitRoutePreview;
            }
            catch {
                current.error = "Engine preview unavailable.";
            }
            if (draft !== current)
                return;
            const error = invalid();
            const s = size();
            if (error || !s || !s.qty) {
                current.busy = false;
                current.error = error || "Zero shares.";
                paint();
                return;
            }
            const args: SubmitRiskEntryArgs = { venue, symbol: latest.current.symbol, buyStop: current.buy!, sellStop: current.sell!, mode: current.template.mode, value: current.template.value,
                maxQty: Math.min(current.maxQty, s.qty), buyCushion: current.template.buyCushion, sellCushion: current.template.sellCushion };
            try {
                const ack = await latest.current.sendCommand("SubmitRiskEntry", args);
                if (draft !== current)
                    return;
                if (ack.ambiguous) {
                    current.unknown = true;
                    current.busy = false;
                    paint();
                }
                else if (ack.status === "accepted")
                    clear();
                else {
                    current.busy = false;
                    current.error = ack.reason ?? "Pair rejected.";
                    paint();
                }
            }
            catch {
                if (draft === current) {
                    current.unknown = true;
                    current.busy = false;
                    paint();
                }
            }
        };
        const initiate = (event: Event) => {
            const p = latest.current, detail = (event as CustomEvent<{
                template: RiskEntryTemplate;
                handled: boolean;
            }>).detail;
            if (!p.active || !p.configLoaded || p.group === null || !venue || !p.symbol || !document.hasFocus() || modalTracker.isOpen() || editable() || p.activeTool !== "select" || p.chooserOpenRef.current)
                return;
            detail.handled = true;
            if (draft?.busy || draft?.unknown)
                return;
            clear();
            draft = { template: detail.template, complete: false, busy: false, unknown: false, maxQty: 0 };
            p.facadeRef.current?.setPanZoomEnabled?.(false);
            paint();
            const current = draft;
            void p.sendQuery("QueryStopLimitRoute", { tif: "DAY", session: "EXTENDED", symbol: p.symbol, riskEntry: true }).then(raw => {
                if (draft !== current)
                    return;
                current.route = raw as StopLimitRoutePreview;
                paint();
            }).catch(() => { if (draft === current) {
                current.error = "Engine preview unavailable.";
                paint();
            } });
        };
        const priceAt = (e: PointerEvent) => {
            const rect = host.getBoundingClientRect(), facade = latest.current.facadeRef.current, x = e.clientX - rect.left, y = e.clientY - rect.top;
            if (!facade || x < 0 || y < 0 || x >= rect.width - facade.priceScaleWidth() || y >= (facade.paneHeights()[0] ?? 0))
                return null;
            const price = facade.coordinateToPrice(y);
            return price != null && Number.isFinite(price) && price > 0 ? snapOrderMarkerPrice(price) : null;
        };
        const consume = (e: Event) => { e.preventDefault(); e.stopImmediatePropagation(); };
        const down = (e: PointerEvent) => {
            if (draft && e.button === 2) {
                consume(e);
                clear();
                return;
            }
            if (!draft || draft.busy || draft.unknown || e.button !== 0 || (e.target as Element)?.closest?.("[data-drawing-ui],[data-order-markers],input,button,select,textarea"))
                return;
            if (modalTracker.isOpen() || !document.hasFocus()) {
                clear();
                return;
            }
            const price = priceAt(e);
            if (price == null)
                return;
            consume(e);
            let endpoint: "buy" | "sell" = "sell";
            if (draft.buy === undefined) {
                endpoint = "buy";
                draft.buy = price;
            }
            else if (draft.complete) {
                const y = e.clientY - host.getBoundingClientRect().top, facade = latest.current.facadeRef.current;
                const by = facade?.priceToCoordinate(draft.buy), sy = facade?.priceToCoordinate(draft.sell!);
                if (by != null && Math.abs(by - y) <= 8)
                    endpoint = "buy";
                else if (sy == null || Math.abs(sy - y) > 8)
                    return;
                draft[endpoint] = price;
            }
            else
                draft.sell = price;
            pointer = { id: e.pointerId, x: e.clientX, y: e.clientY, endpoint, moved: false, second: endpoint === "sell" };
            paint();
        };
        const move = (e: PointerEvent) => {
            if (!draft || draft.busy || draft.unknown)
                return;
            if (pointer && e.pointerId !== pointer.id)
                return;
            const price = priceAt(e);
            if (price == null)
                return;
            if (pointer) {
                consume(e);
                if (Math.hypot(e.clientX - pointer.x, e.clientY - pointer.y) >= 3)
                    pointer.moved = true;
                const endpoint = pointer.endpoint === "buy" && !draft.complete ? "sell" : pointer.endpoint;
                if (pointer.moved)
                    draft[endpoint] = price;
            }
            else if (draft.buy !== undefined && !draft.complete)
                draft.sell = price;
            schedule();
        };
        const up = (e: PointerEvent) => {
            if (!draft || !pointer || pointer.id !== e.pointerId)
                return;
            consume(e);
            const completed = pointer.second || pointer.moved;
            pointer = null;
            if (completed && draft.sell !== undefined) {
                draft.complete = true;
                draft.maxQty = size()?.qty ?? 0;
                paint();
                if (latest.current.config.chartRiskAutoSend && !invalid())
                    void submit();
            }
            else
                paint();
        };
        const key = (e: KeyboardEvent) => {
            if (!draft || editable() || modalTracker.isOpen())
                return;
            if (e.key === "Escape") {
                consume(e);
                clear();
            }
            else if (e.key === "Enter" && !e.repeat) {
                consume(e);
                void submit();
            }
        };
        const click = (e: MouseEvent) => { if ((e.target as Element)?.closest("[data-risk-send]"))
            void submit(); if ((e.target as Element)?.closest("[data-risk-cancel]"))
            clear(); };
        window.addEventListener(CHART_RISK_ENTRY_EVENT, initiate);
        host.addEventListener("pointerdown", down, true);
        window.addEventListener("pointermove", move, true);
        window.addEventListener("pointerup", up, true);
        window.addEventListener("keydown", key, true);
        window.addEventListener("blur", clear);
        window.addEventListener("pointercancel", clear);
        rootRef.current?.addEventListener("click", click);
        const unsubscribe = latest.current.stores.exec.subscribe(schedule);
        const timer = setInterval(() => { if (draft)
            paint(); }, 1000);
        return () => {
            clear();
            if (frame !== null)
                cancelAnimationFrame(frame);
            clearInterval(timer);
            unsubscribe();
            window.removeEventListener(CHART_RISK_ENTRY_EVENT, initiate);
            host.removeEventListener("pointerdown", down, true);
            window.removeEventListener("pointermove", move, true);
            window.removeEventListener("pointerup", up, true);
            window.removeEventListener("keydown", key, true);
            window.removeEventListener("blur", clear);
            window.removeEventListener("pointercancel", clear);
            rootRef.current?.removeEventListener("click", click);
        };
    }, [props.active, props.group, props.symbol, props.contextKey, props.activeTool, props.config.templates, venue]);
    return <div ref={rootRef} data-testid="chart-risk-entry" style={{ display: "none", position: "absolute", inset: 0, zIndex: 10, pointerEvents: "none" }}>
    <div data-risk-buy style={{ position: "absolute", left: 0, borderTop: "2px dashed #34c6dc", color: "#34c6dc", fontSize: 11 }}/>
    <div data-risk-sell style={{ position: "absolute", left: 0, borderTop: "2px dashed #ff6877", color: "#ff6877", fontSize: 11 }}/>
    <div data-drawing-ui style={{ position: "absolute", top: 10, left: 10, maxWidth: "80%", padding: 8, background: "#0c1017", color: "#ddd", border: "1px solid #34c6dc", fontSize: 11, pointerEvents: "auto" }}>
      <div data-risk-detail aria-live="polite" style={{ whiteSpace: "pre-wrap" }}/>
      <button data-risk-send type="button">Send pair</button> <button data-risk-cancel type="button">Cancel setup</button>
    </div>
  </div>;
}
