import { useEffect, useRef, type MutableRefObject } from "react";
import type { AckMsg, StopLimitRoutePreview, SubmitRiskEntryArgs } from "../../../wire/contract";
import type { Stores } from "../../../data/registry";
import type { ChartApiFacade } from "../../../render/chart/ChartApiFacade";
import { nearestPriceLines, orderMarkerChipYs, snapOrderMarkerPrice } from "../../../render/chart/orderMarkers";
import { TV_FONT, type TvChrome } from "../../../render/chart/tvTheme";
import type { Tool } from "../../../render/chart/drawings/interaction";
import { CHART_RISK_ENTRY_EVENT, type OrderConfig, type RiskEntryTemplate } from "../../exec/actionTemplate";
import { riskEntrySize } from "../../exec/riskEntry";
import type { LinkGroup, LinkGroups } from "../../linkGroups";
import { modalTracker } from "../../modalTracker";
interface Props {
    chrome: TvChrome;
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
    layoutRef: MutableRefObject<() => void>;
    sendCommand(name: string, args: unknown): Promise<AckMsg>;
    sendQuery(name: string, args: unknown): Promise<unknown>;
}
type Draft = {
    template: RiskEntryTemplate;
    buy?: number;
    sell?: number;
    buyX?: number;
    sellX?: number;
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
            axis: boolean;
            price: number;
            editing: boolean;
        } | null = null;
        let frame: number | null = null;
        let hover: PointerEvent | null = null;
        const cursor = (endpoint: "buy" | "sell" | null) => {
            if (!endpoint && host.dataset.orderEntryMode !== "risk") return;
            if (endpoint) host.dataset.orderEntryMode = "risk";
            else if (host.dataset.orderEntryMode === "risk") delete host.dataset.orderEntryMode;
            latest.current.facadeRef.current?.setOrderCrosshair?.(endpoint ? endpoint === "buy" ? latest.current.chrome.up : latest.current.chrome.down : null);
            if (!endpoint) { host.title = ""; host.style.cursor = ""; }
        };
        const message = (text: string) => { const el = rootRef.current?.querySelector<HTMLElement>("[data-risk-detail]"); if (el)
            el.textContent = text; };
        const clear = () => { if (draft && latest.current.activeTool === "select") latest.current.facadeRef.current?.setPanZoomEnabled?.(true); draft = null; pointer = null; hover = null;
            delete host.dataset.riskEntryActive; cursor(null); latest.current.chooserOpenRef.current = false;
            const chooser = rootRef.current?.querySelector<HTMLElement>("[data-risk-chooser]"); if (chooser) chooser.style.display = "none";
            if (rootRef.current)
            rootRef.current.style.display = "none"; };
        const editable = () => !!document.activeElement?.closest("input,textarea,select,[contenteditable='true']");
        const size = () => {
            const a = latest.current.stores.exec.accounts().find(a => a.venue === venue);
            return draft && riskEntrySize(draft.template, draft.buy ?? 0, draft.sell ?? 0, a?.availableCash ?? 0, a?.buyingPower ?? 0);
        };
        const sizingError = () => {
            const a = latest.current.stores.exec.accounts().find(a => a.venue === venue);
            if (!a || a.tsMs <= 0 || Date.now() - a.tsMs > 30000 || a.tsMs > Date.now() + 1000)
                return "Fresh account data required.";
            if (!draft) return "";
            if (!Number.isFinite(draft.template.value) || draft.template.value <= 0 || (draft.template.mode !== "Dollar" && draft.template.value > 100))
                return "Positive risk value required (percentages at most 100).";
            if (draft.buy !== undefined && draft.sell !== undefined) {
                if (draft.sell >= draft.buy) return "Sell trigger must be below buy trigger.";
                if (!size()?.qty || (draft.complete && !draft.maxQty)) return "Risk or funding budget rounds to zero shares.";
            }
            return "";
        };
        const invalid = () => {
            const p = latest.current, status = p.stores.exec.status(), v = status?.venues.find(v => v.venue === venue);
            if (!p.active || !document.hasFocus() || modalTracker.isOpen() || editable())
                return "Focus the chart to continue.";
            if (!status?.masterArmed)
                return "Trading is locked. Arm the engine first.";
            if (!v?.connected || v.reconcilePending || v.flattenPending || !v.positionDataReady)
                return "Venue or position data unavailable; reconcile first.";
            if (v.env === "live" && !v.heldStopLimitAcknowledged)
                return "Review / enable live accounts in Settings → Orders & hotkeys.";
            const sizing = sizingError();
            if (sizing) return sizing;
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
            return "";
        };
        const paint = () => {
            if (!draft || !rootRef.current)
                return;
            rootRef.current.style.display = "block";
            const facade = latest.current.facadeRef.current;
            const bar = rootRef.current.querySelector<HTMLElement>("[data-risk-bar]");
            if (bar) {
                // Volume shares the main pane; indicator panes and the time axis sit below it.
                bar.style.top = `${Math.max(0, (facade?.paneHeights()[0] ?? host.clientHeight) - 6)}px`;
                bar.style.right = `${(facade?.priceScaleWidth() ?? 60) + 8}px`;
            }
            const s = size(), error = invalid();
            const qty = s ? draft.complete ? Math.min(draft.maxQty, s.qty) : s.qty : 0;
            const paneHeight = facade?.paneHeights()[0] ?? host.clientHeight;
            const plotWidth = Math.max(0, host.getBoundingClientRect().width - (facade?.priceScaleWidth() ?? 60));
            const buyY = draft.buy === undefined ? null : facade?.priceToCoordinate(draft.buy);
            const sellY = draft.sell === undefined ? null : facade?.priceToCoordinate(draft.sell);
            const visible = buyY != null && sellY != null && Number.isFinite(buyY) && Number.isFinite(sellY)
                && buyY >= 0 && sellY >= 0 && buyY < paneHeight && sellY < paneHeight
                && draft.buyX !== undefined && draft.sellX !== undefined
                && (draft.complete || (hover && priceAt(hover) != null));
            const arrow = rootRef.current.querySelector<SVGSVGElement>("[data-risk-arrow]");
            const readout = rootRef.current.querySelector<HTMLElement>("[data-risk-readout]");
            if (arrow) arrow.style.display = visible ? "block" : "none";
            if (readout) readout.style.display = visible ? "block" : "none";
            if (visible && arrow && readout) {
                const x = Math.max(4, Math.min(plotWidth - 4, (draft.buyX! + draft.sellX!) / 2));
                const direction = sellY >= buyY ? 1 : -1;
                const headY = sellY - direction * Math.min(6, Math.abs(sellY - buyY));
                arrow.style.width = `${plotWidth}px`;
                arrow.style.height = `${paneHeight}px`;
                arrow.querySelector("path")?.setAttribute("d", `M ${x} ${buyY} V ${sellY} M ${x - 4} ${headY} L ${x} ${sellY} L ${x + 4} ${headY}`);
                readout.textContent = sizingError() || !s ? "— shares · Est. risk —" :
                    `${qty.toLocaleString("en-US")} shares · Est. risk ${(s.qty ? s.risk * qty / s.qty : 0).toLocaleString("en-US", { style: "currency", currency: "USD" })}`;
                readout.style.maxWidth = `${Math.max(0, plotWidth - 16)}px`;
                readout.style.left = `${Math.max(4, Math.min(x + 10, plotWidth - readout.offsetWidth - 12))}px`;
                readout.style.top = `${Math.max(4, Math.min((buyY + sellY) / 2 - readout.offsetHeight / 2, paneHeight - readout.offsetHeight - 4))}px`;
            }
            const deadline = draft.route?.deadlineMs ? new Intl.DateTimeFormat("en-US", { timeZone: "America/New_York", hour: "numeric", minute: "2-digit", timeZoneName: "short" }).format(draft.route.deadlineMs) : "checking";
            const endpoints = (["buy", "sell"] as const).filter(endpoint => draft![endpoint] !== undefined && (endpoint === "buy" || draft!.complete));
            const chipYs = orderMarkerChipYs(endpoints.map(endpoint => facade?.priceToCoordinate(draft![endpoint]!) ?? -10000), facade?.paneHeights()[0] ?? host.clientHeight);
            for (const endpoint of ["buy", "sell"] as const) {
                const line = rootRef.current.querySelector<HTMLElement>(`[data-risk-${endpoint}]`);
                const chip = rootRef.current.querySelector<HTMLElement>(`[data-risk-chip='${endpoint}']`);
                const price = draft[endpoint], y = !endpoints.includes(endpoint) || price === undefined ? null : facade?.priceToCoordinate(price);
                if (line) {
                    line.style.display = y == null ? "none" : "block";
                    line.style.top = `${y}px`;
                    line.style.right = `${facade?.priceScaleWidth() ?? 60}px`;
                }
                if (chip) {
                    chip.style.display = y == null ? "none" : "flex";
                    chip.style.top = `${chipYs[endpoints.indexOf(endpoint)]}px`;
                    const button = chip.querySelector<HTMLButtonElement>("[data-risk-price]");
                    if (button && price !== undefined) {
                        button.textContent = `${endpoint === "buy" ? "B" : "S"} ${price.toFixed(price < 1 ? 4 : 2)}`;
                        const limit = endpoint === "buy" ? s?.buyLimit : s?.sellLimit;
                        button.title = `${draft.complete ? `${qty.toLocaleString("en-US")} planned shares` : "Choose both prices to size shares"}\n${endpoint === "buy" ? "BUY" : "SELL"} STOP-LIMIT · limit ${limit?.toFixed(limit < 1 ? 4 : 2) ?? "pending"}\nHeld by eTape · DAY ${deadline} · fees/execution risk excluded\n${draft.busy || draft.unknown ? "" : "Enter send · "}Esc cancel`;
                        button.setAttribute("aria-label", `${endpoint === "buy" ? "Buy" : "Sell"} trigger ${price.toFixed(price < 1 ? 4 : 2)}; drag or use arrow keys to adjust`);
                        button.disabled = draft.busy || draft.unknown;
                    }
                    const cancel = chip.querySelector<HTMLButtonElement>("[data-risk-cancel]");
                    if (cancel) cancel.disabled = draft.busy;
                }
            }
            message(draft.buy === undefined ? "Click BUY, then SELL; or drag. Esc cancel." : !draft.complete ? "Choose SELL below BUY. Esc cancel." : "");
            if (draft.busy || draft.unknown) cursor(null);
            else if (pointer || (hover && priceAt(hover) != null)) {
                const endpoint = pointer ? pointer.endpoint === "buy" && !pointer.editing && pointer.moved ? "sell" : pointer.endpoint : draft.complete ? null : draft.buy === undefined ? "buy" : "sell";
                cursor(endpoint);
                if (endpoint) {
                    const candidate = pointer ? draft[endpoint] : hover && priceAt(hover);
                    const limit = endpoint === "buy" ? s?.buyLimit : s?.sellLimit;
                    host.title = `${draft.complete ? `${qty.toLocaleString("en-US")} planned shares` : "Choose both prices to size shares"}\n${endpoint === "buy" ? "BUY" : "SELL"} STOP-LIMIT · trigger ${candidate ?? "pending"} · limit ${limit ?? "pending"}\nHeld by eTape · DAY ${deadline}\n${venue} · fees/execution risk excluded\nEsc cancel`;
                }
            }
            const status = rootRef.current.querySelector<HTMLElement>("[data-risk-status]");
            if (status) {
                status.textContent = draft.unknown ? "Submit outcome unknown. Verify Orders; this setup cannot be resent." : draft.busy ? "Submitting linked pair…" :
                    [error, draft.error !== error ? draft.error : "", draft.route?.lastEligiblePrice && draft.buy !== undefined && draft.route.lastEligiblePrice >= draft.buy ? "BUY WILL TRIGGER NOW" : ""].filter(Boolean).join(" · ");
                status.style.display = status.textContent ? "block" : "none";
                status.style.color = draft.busy ? latest.current.chrome.text : latest.current.chrome.down;
            }
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
            host.dataset.riskEntryActive = "true";
            cursor("buy");
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
            const chip = (e.target as Element)?.closest?.<HTMLElement>("[data-risk-price],[data-risk-choice]");
            if (!draft || draft.busy || draft.unknown || e.button !== 0 || (!chip && (e.target as Element)?.closest?.("[data-drawing-ui],[data-order-markers],input,button,select,textarea")))
                return;
            if (modalTracker.isOpen() || !document.hasFocus()) {
                clear();
                return;
            }
            const endpointAtChip = (chip?.dataset.riskPrice ?? chip?.dataset.riskChoice) as "buy" | "sell" | undefined;
            const price = endpointAtChip ? draft[endpointAtChip] ?? null : priceAt(e);
            if (price == null)
                return;
            const facade = latest.current.facadeRef.current;
            const selected = (["buy", "sell"] as const).filter(endpoint => draft![endpoint] !== undefined && (endpoint === "buy" || draft!.complete))
                .map(endpoint => ({ endpoint, price: draft![endpoint]! }));
            const hits = chip ? [] : nearestPriceLines(selected, e.clientY - host.getBoundingClientRect().top, p => facade?.priceToCoordinate(p) ?? null);
            if (hits.length > 1) {
                consume(e);
                const chooser = rootRef.current?.querySelector<HTMLElement>("[data-risk-chooser]");
                if (chooser) { chooser.style.display = "flex"; chooser.style.top = `${e.clientY - host.getBoundingClientRect().top}px`; }
                latest.current.chooserOpenRef.current = true;
                return;
            }
            if (draft.complete && !chip && !hits.length) return;
            consume(e);
            const editing = !!chip || hits.length === 1;
            let endpoint: "buy" | "sell" = "sell";
            if (editing) {
                endpoint = endpointAtChip ?? hits[0].endpoint;
                const chooser = rootRef.current?.querySelector<HTMLElement>("[data-risk-chooser]"); if (chooser) chooser.style.display = "none";
                latest.current.chooserOpenRef.current = false;
            }
            else if (draft.buy === undefined) {
                endpoint = "buy";
                draft.buy = price;
                draft.buyX = e.clientX - host.getBoundingClientRect().left;
            }
            else {
                draft.sell = price;
                draft.sellX = e.clientX - host.getBoundingClientRect().left;
            }
            hover = e;
            pointer = { id: e.pointerId, x: e.clientX, y: e.clientY, endpoint, moved: false, second: !editing && endpoint === "sell", axis: editing, price: editing ? draft[endpoint]! : price, editing };
            paint();
        };
        const move = (e: PointerEvent) => {
            if (!draft || draft.busy || draft.unknown)
                return;
            if (pointer && e.pointerId !== pointer.id)
                return;
            hover = e;
            if (!pointer && (e.target as Element)?.closest?.("[data-drawing-ui],button,input,select,textarea")) { hover = null; cursor(null); schedule(); return; }
            const facade = latest.current.facadeRef.current;
            const raw = pointer?.axis ? facade?.coordinateToPrice((facade.priceToCoordinate(pointer.price) ?? pointer.y - host.getBoundingClientRect().top) + e.clientY - pointer.y) : priceAt(e);
            const price = raw != null && Number.isFinite(raw) && raw > 0 ? snapOrderMarkerPrice(raw) : null;
            if (price == null) { cursor(null); schedule(); return; }
            if (pointer) {
                consume(e);
                if (Math.hypot(e.clientX - pointer.x, e.clientY - pointer.y) >= 3)
                    pointer.moved = true;
                const endpoint = pointer.endpoint === "buy" && !draft.complete && !pointer.editing ? "sell" : pointer.endpoint;
                if (pointer.moved) {
                    draft[endpoint] = price;
                    if (!pointer.editing) draft.sellX = e.clientX - host.getBoundingClientRect().left;
                }
            }
            else if (draft.buy !== undefined && !draft.complete) {
                draft.sell = price;
                draft.sellX = e.clientX - host.getBoundingClientRect().left;
            }
            if (!pointer) {
                const hits = nearestPriceLines((["buy", "sell"] as const).filter(endpoint => draft![endpoint] !== undefined && (endpoint === "buy" || draft!.complete)).map(endpoint => ({ price: draft![endpoint]! })),
                    e.clientY - host.getBoundingClientRect().top, p => facade?.priceToCoordinate(p) ?? null);
                host.style.cursor = hits.length ? "ns-resize" : "";
                if (draft.complete) host.title = hits.length ? rootRef.current?.querySelector<HTMLButtonElement>(`[data-risk-price='${draft.buy === hits[0].price ? "buy" : "sell"}']`)?.title ?? "" : "";
            }
            schedule();
        };
        const up = (e: PointerEvent) => {
            if (!draft || !pointer || pointer.id !== e.pointerId)
                return;
            consume(e);
            const completed = !pointer.editing && (pointer.second || pointer.moved);
            pointer = null;
            if (completed && draft.sell !== undefined) {
                draft.complete = true;
                draft.maxQty = size()?.qty ?? 0;
                paint();
                if (latest.current.config.chartRiskAutoSend && !invalid())
                    void submit();
            }
            else
                { draft.maxQty = size()?.qty ?? 0; paint(); }
        };
        const key = (e: KeyboardEvent) => {
            if (!draft || editable() || modalTracker.isOpen())
                return;
            if ((e.target as Element)?.closest?.("[data-risk-cancel]") && (e.key === "Enter" || e.key === " "))
                return;
            const chip = (e.target as Element)?.closest?.<HTMLElement>("[data-risk-price]");
            if (chip && !draft.busy && !draft.unknown && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
                consume(e);
                const endpoint = chip.dataset.riskPrice as "buy" | "sell", price = draft[endpoint];
                if (price !== undefined) {
                    const next = snapOrderMarkerPrice(price + (e.key === "ArrowUp" ? 1 : -1) * (price >= 1 ? 0.01 : 0.0001));
                    if (next > 0) draft[endpoint] = next;
                    draft.maxQty = size()?.qty ?? 0;
                    paint();
                }
                return;
            }
            if (e.key === "Escape") {
                consume(e);
                clear();
            }
            else if (e.key === "Enter" && !e.repeat) {
                consume(e);
                void submit();
            }
        };
        const click = (e: MouseEvent) => { if ((e.target as Element)?.closest?.("[data-risk-cancel]")) clear(); };
        const leave = () => { hover = null; cursor(null); schedule(); };
        window.addEventListener(CHART_RISK_ENTRY_EVENT, initiate);
        host.addEventListener("pointerdown", down, true);
        host.addEventListener("pointerleave", leave);
        window.addEventListener("pointermove", move, true);
        window.addEventListener("pointerup", up, true);
        window.addEventListener("keydown", key, true);
        window.addEventListener("blur", clear);
        window.addEventListener("pointercancel", clear);
        rootRef.current?.addEventListener("click", click);
        const resize = new ResizeObserver(schedule);
        resize.observe(host);
        props.layoutRef.current = paint;
        const unsubscribe = latest.current.stores.exec.subscribe(schedule);
        const timer = setInterval(() => { if (draft)
            paint(); }, 1000);
        return () => {
            clear();
            if (props.layoutRef.current === paint) props.layoutRef.current = () => {};
            if (frame !== null)
                cancelAnimationFrame(frame);
            clearInterval(timer);
            resize.disconnect();
            unsubscribe();
            window.removeEventListener(CHART_RISK_ENTRY_EVENT, initiate);
            host.removeEventListener("pointerdown", down, true);
            host.removeEventListener("pointerleave", leave);
            window.removeEventListener("pointermove", move, true);
            window.removeEventListener("pointerup", up, true);
            window.removeEventListener("keydown", key, true);
            window.removeEventListener("blur", clear);
            window.removeEventListener("pointercancel", clear);
            rootRef.current?.removeEventListener("click", click);
        };
    }, [props.active, props.group, props.symbol, props.contextKey, props.activeTool, props.config.templates, props.layoutRef, venue]);
    return <div ref={rootRef} data-testid="chart-risk-entry" style={{ display: "none", position: "absolute", inset: 0, zIndex: 10, pointerEvents: "none" }}>
    <div data-risk-buy style={{ position: "absolute", left: 0, borderTop: `2px dashed ${props.chrome.up}` }}/>
    <div data-risk-sell style={{ position: "absolute", left: 0, borderTop: `2px dashed ${props.chrome.down}` }}/>
    <svg data-risk-arrow aria-hidden="true" style={{ display: "none", position: "absolute", left: 0, top: 0, overflow: "hidden", pointerEvents: "none" }}>
      <path fill="none" stroke={props.chrome.down} strokeWidth={1.5} />
    </svg>
    <div data-risk-readout style={{ display: "none", position: "absolute", color: props.chrome.text, fontFamily: TV_FONT, fontSize: 12, lineHeight: "16px", textShadow: `0 0 2px ${props.chrome.bg}, 0 0 4px ${props.chrome.bg}`, pointerEvents: "none", overflowWrap: "anywhere" }} />
    <div data-risk-chooser role="dialog" aria-label="Choose draft price" style={{ display:"none",position:"absolute",right:70,pointerEvents:"auto",background:props.chrome.bg,border:`1px solid ${props.chrome.muted}`,padding:4,gap:4 }}>
      <button type="button" data-risk-choice="buy">BUY</button><button type="button" data-risk-choice="sell">SELL</button>
    </div>
    {(["buy", "sell"] as const).map(endpoint => <div key={endpoint} data-risk-chip={endpoint} data-drawing-ui style={{ display:"none",position:"absolute",right:0,transform:"translateY(-50%)",height:20,boxSizing:"border-box",border:`1px solid ${endpoint === "buy" ? props.chrome.up : props.chrome.down}`,borderRadius:3,background:"#0c1017",color:endpoint === "buy" ? props.chrome.up : props.chrome.down,pointerEvents:"auto",font:"600 10px ui-monospace,monospace",whiteSpace:"nowrap" }}>
      <button type="button" data-risk-price={endpoint} style={{border:0,background:"transparent",color:"inherit",font:"inherit",padding:"0 5px",cursor:"ns-resize"}} />
      <button type="button" data-risk-cancel aria-label="Discard risk setup" title="Discard both draft prices" style={{border:0,borderLeft:"1px solid currentColor",background:"transparent",color:"inherit",font:"bold 12px system-ui",padding:"0 5px",cursor:"pointer"}}>×</button>
    </div>)}
    <div data-risk-bar aria-live="polite" style={{ position: "absolute", left: 44, transform: "translateY(-100%)", color: props.chrome.text, fontFamily: TV_FONT, fontSize: 11, lineHeight: "14px", textShadow: `0 0 2px ${props.chrome.bg}, 0 0 4px ${props.chrome.bg}`, pointerEvents: "none", overflowWrap: "anywhere" }}>
      <div data-risk-detail style={{ whiteSpace: "pre-wrap" }}/>
      <div data-risk-status />
    </div>
  </div>;
}
