import { useEffect, useRef, type MutableRefObject } from "react";
import type { AckMsg, StopLimitRoutePreview, SubmitRiskEntryArgs } from "../../../wire/contract";
import type { PerpetualOpeningPreview } from "../../../gen/wsmsg";
import { isWorking } from "../../../wire/orderStatus";
import type { Stores } from "../../../data/registry";
import type { ChartApiFacade } from "../../../render/chart/ChartApiFacade";
import { nearestPriceLines, orderMarkerChipYs, snapOrderMarkerPrice } from "../../../render/chart/orderMarkers";
import { TV_FONT, type TvChrome } from "../../../render/chart/tvTheme";
import type { Tool } from "../../../render/chart/drawings/interaction";
import { CHART_RISK_ENTRY_EVENT, defaultLeverageCap, type OrderConfig, type RiskEntryTemplate } from "../../exec/actionTemplate";
import { riskEntrySize, riskEntryProtection, riskEntryAddTemplate } from "../../exec/riskEntry";
import { perpetualRiskSize, perpetualTick } from "../../exec/perpetual";
import {trailError} from "../../exec/trailing";
import type { LinkGroup, LinkGroups } from "../../linkGroups";
import { modalTracker } from "../../modalTracker";
import { useToasts } from "../../Toast";
import { attachOrderPillLayer } from "./orderPillLayer";
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
	sharedEntryId: string;
    template: RiskEntryTemplate;
    buy?: number;
    sell?: number;
    buyX?: number;
    sellX?: number;
    complete: boolean;
    stockShort?: boolean;
    busy: boolean;
    unknown: boolean;
    maxQty: number;
    route?: StopLimitRoutePreview;
    error?: string;
};
export function ChartRiskEntry(props: Props): JSX.Element {
    const { push } = useToasts();
    const rootRef = useRef<HTMLDivElement | null>(null);
    const pillLayer = useRef<ReturnType<typeof attachOrderPillLayer> | null>(null);
    useEffect(() => {
        if (!props.hostRef.current || !rootRef.current) return;
        const owner = attachOrderPillLayer(props.hostRef.current, rootRef.current);
        pillLayer.current = owner;
        return () => { owner.dispose(); pillLayer.current = null; };
    }, [props.hostRef]);
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
        let lastPoint: PointerEvent | null = null;
        let opening: PerpetualOpeningPreview | undefined;
        let warming = false;
        let disposed = false;
        const perpetualSide = () => {
            const perp = draft?.template.perpetual;
            if (!perp || !draft) return undefined;
            if (!draft.template.auto || draft.complete) return perp.side;
            if (draft.buy === undefined || draft.sell === undefined || draft.buy === draft.sell) return undefined;
            return draft.sell > draft.buy ? "SHORT" : "BUY";
        };
        const stockBroker = () => latest.current.stores.exec.status()?.venues.find(v => v.venue === venue)?.broker;
        const canDrawStockShort = () => draft?.template.auto && !draft.template.perpetual && ["sim", "alpaca"].includes(stockBroker() ?? "");
        const short = () => {
            if (draft?.template.perpetual) return perpetualSide() === "SHORT";
            if (draft?.complete) return draft.stockShort === true;
            return !!canDrawStockShort() && draft?.buy !== undefined && draft.sell !== undefined && draft.sell > draft.buy;
        };
        const cursor = (endpoint: "buy" | "sell" | null) => {
            if (!endpoint && host.dataset.orderEntryMode !== "risk") return;
            if (endpoint) host.dataset.orderEntryMode = "risk";
            else if (host.dataset.orderEntryMode === "risk") delete host.dataset.orderEntryMode;
            const color = endpoint ? (endpoint === "buy") !== short() ? latest.current.chrome.up : latest.current.chrome.down : null;
            if (endpoint && hover) latest.current.facadeRef.current?.setOrderCrosshair?.(color, hover);
            else latest.current.facadeRef.current?.setOrderCrosshair?.(color);
            if (!endpoint) { host.title = ""; host.style.cursor = ""; }
        };
        const message = (text: string) => { const el = rootRef.current?.querySelector<HTMLElement>("[data-risk-detail]"); if (el)
            el.textContent = text; };
        const clear = () => { if (draft && latest.current.activeTool === "select") latest.current.facadeRef.current?.setPanZoomEnabled?.(true); draft = null; pointer = null; hover = null;
            delete host.dataset.riskEntryActive; cursor(null); latest.current.chooserOpenRef.current = false;
            const chooser = rootRef.current?.querySelector<HTMLElement>("[data-risk-chooser]"); if (chooser) chooser.style.display = "none";
            if (rootRef.current)
            rootRef.current.style.display = "none"; pillLayer.current?.clear(); pillLayer.current?.sync(); };
        const editable = () => !!document.activeElement?.closest("input,textarea,select,[contenteditable='true']");
        const executionVenue = () => latest.current.stores.exec.status()?.venues.find(v => v.venue === venue);
        const bingx = () => latest.current.symbol.startsWith("PERP.BINGX.") &&
            ["bingx-vst", "bingx-live"].includes(executionVenue()?.broker ?? "");
        const livePerpetual = () => !!draft?.template.perpetual && executionVenue()?.env === "live" &&
            (executionVenue()?.broker === "mexc" || bingx());
        const needsOpening = () => bingx() || (props.symbol.startsWith("PERP.MEXC.") && executionVenue()?.broker === "mexc");
        const openingLimit = () => {
            const side = perpetualSide();
            return side === "BUY" ? opening?.long : side === "SHORT" ? opening?.short : undefined;
        };
        const contract = () => latest.current.stores.quote.get(latest.current.symbol)?.contract;
        const snap = (value: number) => {
            const rules = draft?.template.perpetual ? contract() : undefined;
            return rules ? perpetualTick(value, rules) : snapOrderMarkerPrice(value);
        };
        const leg = (endpoint: "buy" | "sell") => {
            if (draft?.template.auto && (draft.template.perpetual ? !perpetualSide() : canDrawStockShort() && (!draft.buy || !draft.sell || draft.buy === draft.sell))) return endpoint === "buy" ? "Entry" : "Stop";
            return endpoint === "buy" ? short() ? "SHORT" : "BUY" : short() ? "COVER" : "SELL";
        };
        const pxText = (price: number) => draft?.template.perpetual ? String(price) : price.toFixed(price < 1 ? 4 : 2);
        const positionQty = () => latest.current.stores.exec.positions()
            .find(pos => pos.venue === venue && pos.symbol === props.symbol)?.qty ?? 0;
        const protection = () => {
            const p = latest.current;
            const qty = positionQty();
            if (p.symbol.startsWith("PERP.")) {
                const working = p.stores.exec.orders().map(v => v.order)
                    .filter(order => order.venue === venue && order.symbol === p.symbol && isWorking(order.status));
                if (bingx() && qty) {
                    const stop = working.find(o => o.wholePositionProtection && o.side === (qty > 0 ? "SELL" : "COVER") &&
                        o.leavesQty === Math.abs(qty) && o.held && ["WAITING", "ARMED"].includes(o.held.phase) &&
                        !o.held.childClientId && !o.held.perpetualTriggered && !o.held.cancelRequested);
                    if (stop && working.length === 1) return { qty, owner: stop, stop, error: undefined };
                    return { qty, owner: stop, stop, error: "An add needs the full shared stop and no other working orders." };
                }
                return { qty, owner: undefined, stop: undefined, error: qty || working.length ? "Start from flat with no working orders." : undefined };
            }
            return { ...riskEntryProtection(p.stores.exec.orders().map(v => v.order), qty, venue, p.symbol), qty };
        };
        const size = (reviewedOnly = true) => {
            const a = latest.current.stores.exec.accounts().find(a => a.venue === venue);
            if (draft?.template.perpetual) {
                const side = perpetualSide();
                if (!side) return null;
                const terms = openingLimit();
                if (needsOpening() && !terms?.leverage || bingx() && !opening?.notional) return null;
                const template = { ...draft.template, perpetual: { ...draft.template.perpetual, side } };
                const available = Math.min(a?.availableCash ?? 0, opening?.notional ? opening.collateral ?? 0 : Infinity);
                return perpetualRiskSize(template, contract(), draft.buy ?? 0, draft.sell ?? 0, available,
                    reviewedOnly && draft.complete && !pointer ? draft.maxQty : undefined, terms, opening?.notional);
            }
            const context = protection();
            let shortFunds = (a?.buyingPower ?? 0) / 1.03;
            if (short() && stockBroker() === "sim") {
                const store = latest.current.stores.exec;
                shortFunds = a?.equity ?? a?.buyingPower ?? 0;
                for (const position of store.positions()) {
                    if (position.venue === venue) shortFunds -= Math.abs(position.qty) * position.avgPrice;
                }
                for (const { order } of store.orders()) {
                    if (order.venue === venue && isWorking(order.status) && (order.side === "BUY" || order.side === "SHORT") && !order.riskEntryId)
                        shortFunds -= order.leavesQty * Math.max(order.stopPrice ?? 0, order.limitPrice ?? 0);
                }
            }
            const stock = draft && riskEntrySize(riskEntryAddTemplate(draft.template, context.owner, context.stop),
                draft.buy ?? 0, draft.sell ?? 0, a?.availableCash ?? 0, a?.buyingPower ?? 0, undefined, short(), shortFunds);
            return stock && { ...stock, kind: "stock" as const };
        };
        const sizingError = () => {
            const a = latest.current.stores.exec.accounts().find(a => a.venue === venue);
            if (!a || a.tsMs <= 0 || Date.now() - a.tsMs > 30000 || a.tsMs > Date.now() + 1000)
                return "Fresh account data required.";
            if (!draft) return "";
            if (!Number.isFinite(draft.template.value) || draft.template.value <= 0 || (draft.template.mode !== "Dollar" && draft.template.value > 100))
                return "Positive risk value required (percentages at most 100).";
            if (draft.buy !== undefined && draft.sell !== undefined) {
                if (draft.template.auto && draft.sell === draft.buy) return "Choose a stop at a different price from the entry.";
                if (short() ? draft.sell <= draft.buy : draft.sell >= draft.buy)
                    return short() ? "Choose a stop above the short entry." : draft.template.perpetual ? "Choose a stop below the long entry." : "Sell trigger must be below buy trigger.";
                if (needsOpening() && draft.template.perpetual) {
                    if (!opening) return "Waiting for the chart’s opening data.";
                    if (opening.reason) return opening.reason;
                    const terms = openingLimit();
                    if (!terms?.leverage) return "Isolated leverage is unavailable for this direction.";
                    const { leverage, leverageCap, bingxExchangeLeverage } = draft.template.perpetual;
                    if (bingx() && opening.notional?.position.qty !== protection().qty)
                        return "BingX position changed. Review a new chart entry.";
                    if (leverageCap !== undefined || leverage === undefined) {
                        const cap = leverageCap ?? defaultLeverageCap;
                        if (terms.leverage > cap && (!bingx() || !bingxExchangeLeverage))
                            return `Isolated leverage is ${terms.leverage}×, above this preset’s ${cap}× Auto cap.`;
                    } else if (leverageCap === undefined && leverage !== undefined && leverage !== terms.leverage) {
                        return `Isolated leverage is ${terms.leverage}× and this preset uses ${leverage}×. Match the settings before sending.`;
                    }
                    if (terms.maxContracts < (contract()?.minimum ?? 1))
                        return "Isolated opening capacity is below the minimum size.";
                }
                const estimate = size();
                if (!bingx() && estimate?.kind === "perpetual" && estimate.autoLeverage && !contract()?.riskTiers?.length)
                    return "Isolated risk tiers unavailable for Auto leverage.";
                if (!estimate?.qty || (draft.complete && !draft.maxQty))
                    return draft.template.perpetual ? "Margin, leverage or the liquidation buffer cannot support the minimum contract size." : "Risk or funding budget rounds to zero shares.";
            }
            return "";
        };
        const invalid = () => {
            const context = protection();
            if(!context.stop && draft?.template.protection){const error=trailError(draft.template.protection);if(error)return error;}
            const p = latest.current, status = p.stores.exec.status(), v = status?.venues.find(v => v.venue === venue);
            if (!p.active || !document.hasFocus() || modalTracker.isOpen() || editable())
                return "Focus the chart to continue.";
            if (!status?.masterArmed)
                return "Trading is locked. Arm the engine first.";
            if (!v?.connected || v.reconcilePending || v.flattenPending || !v.positionDataReady)
                return "Venue or position data unavailable; reconcile first.";
            if (v.env === "live" && !v.heldStopLimitAcknowledged)
                return draft?.template.perpetual
                    ? `Review ${bingx() ? "BingX" : "MEXC"} Live Chart Risk Entry in Settings → Orders & hotkeys and enable Stop-Limit.`
                    : "Review / enable live accounts in Settings → Orders & hotkeys.";
            if (draft?.template.perpetual && v.env === "live" && status.global.maxDayLoss > 0)
                return "The global USD Max Day Loss limit blocks live perpetual openings.";
            const sizing = sizingError();
            if (sizing) return sizing;
            if (context.error) return context.error;
            if (draft?.template.perpetual) {
                if (!bingx() && v.broker !== "mexc-sim" && v.broker !== "mexc") return "Select a supported perpetual Execution Venue.";
                if (!contract()?.tradingEnabled) return "Waiting for tradable contract rules.";
                if (bingx() && draft.sharedEntryId !== (context.stop?.id ?? "")) return "Shared protection changed; review a new chart entry.";
                if (bingx() && context.stop && draft.sell !== undefined &&
                    (short() ? draft.sell > context.stop.stopPrice : draft.sell < context.stop.stopPrice))
                    return "An add must keep or tighten the shared stop.";
                return "";
            }
            if (draft && draft.sharedEntryId !== (context.owner?.id ?? "")) return "Shared protection changed; review a new chart risk entry.";
            if (context.qty && draft?.sell !== undefined && (short() ? context.qty > 0 : context.qty < 0))
                return "Choose an entry in the direction of the existing position.";
            if (context.stop && draft?.sell !== undefined &&
                (short() ? draft.sell > context.stop.stopPrice + 1e-9 : draft.sell + 1e-9 < context.stop.stopPrice))
                return short() ? "Add stop must keep or lower the shared stop." : "Add stop must keep or raise the shared stop.";
            if (!draft?.route)
                return draft?.error ?? "Checking engine trigger source…";
            if (draft.route.reason)
                return draft.route.reason;
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
            const isLivePerpetual = livePerpetual();
            const { up, down } = latest.current.chrome;
            const isShort = short();
            const qty = s ? draft.complete ? Math.min(draft.maxQty, s.qty) : s.qty : 0;
            const paneHeight = facade?.paneHeights()[0] ?? host.clientHeight;
            const rect = host.getBoundingClientRect();
            const plotWidth = Math.max(0, rect.width - (facade?.priceScaleWidth() ?? 60));
            const inPane = hover && (priceAt(hover) != null || (pointer?.axis && hover.clientX >= rect.left
                && hover.clientX < rect.right && hover.clientY >= rect.top && hover.clientY < rect.top + paneHeight));
            const endpoint = draft.busy || draft.unknown || !inPane ? null : pointer
                ? pointer.endpoint === "buy" && !pointer.editing && pointer.moved ? "sell" : pointer.endpoint
                : draft.complete ? null : draft.buy === undefined ? "buy" : "sell";
            const candidate = endpoint ? pointer || (endpoint === "sell" && draft.sell !== undefined)
                ? draft[endpoint] : priceAt(hover!) : null;
            const candidateY = candidate == null ? null : facade?.priceToCoordinate(candidate);
            const previewVisible = candidateY != null && Number.isFinite(candidateY) && candidateY >= 0 && candidateY < paneHeight;
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
                const path = arrow.querySelector("path");
                path?.setAttribute("d", `M ${x} ${buyY} V ${sellY} M ${x - 4} ${headY} L ${x} ${sellY} L ${x + 4} ${headY}`);
                path?.setAttribute("stroke", isShort ? up : down);
                const amount = s?.qty ? s.risk * qty / s.qty : 0;
                const unit = draft.template.perpetual ? bingx() ? "lots" : "contracts" : "shares";
                const currency = bingx() && executionVenue()?.broker === "bingx-vst" ? "VST" : "USDT";
                readout.textContent = sizingError() || !s ? `— ${unit} · Est. risk —` :
                    `${qty.toLocaleString("en-US")} ${unit} · Est. risk ${s.kind === "perpetual" ? `${amount.toFixed(2)} ${currency} · ${s.leverage}×${s.autoLeverage ? " Auto" : ""}` : amount.toLocaleString("en-US", { style: "currency", currency: "USD" })}`;
                if (s?.kind === "perpetual" && qty > 0) {
                    readout.textContent += `\nEst. initial margin ${(s.margin * qty / s.qty).toFixed(2)} ${currency}`;
                    if (bingx()) {
                        const base = Number(contract()?.multiplier ?? 0) * qty;
                        readout.textContent += ` · ${base.toLocaleString("en-US", { maximumFractionDigits: 10 })} ${contract()?.base ?? "base units"}`;
                    }
                }
                if (draft.template.auto && (perpetualSide() || (!draft.template.perpetual && (isShort || draft.sell! < draft.buy!))))
                    readout.textContent = `${isShort ? "Short" : "Long"} · ${readout.textContent}`;
                const context = protection();
                if (context.stop) {
                    const existing = Math.abs(context.qty);
                    readout.textContent += `\n${existing.toLocaleString("en-US")} existing + ${qty.toLocaleString("en-US")} added = ${(existing + qty).toLocaleString("en-US")} total\nShared stop ${pxText(context.stop.stopPrice)} → ${pxText(draft.sell!)} on first fill · ${context.stop.held?.trail ? "trailing mode retained" : "Fixed"}`;
                }
                readout.style.whiteSpace = "pre-line";
                readout.style.maxWidth = `${Math.max(0, plotWidth - 16)}px`;
                readout.style.left = `${Math.max(4, Math.min(x + 10, plotWidth - readout.offsetWidth - 12))}px`;
                readout.style.top = `${Math.max(4, Math.min((buyY + sellY) / 2 - readout.offsetHeight / 2, paneHeight - readout.offsetHeight - 4))}px`;
            }
            const deadline = draft.route?.deadlineMs ? new Intl.DateTimeFormat("en-US", { timeZone: "America/New_York", hour: "numeric", minute: "2-digit", timeZoneName: "short" }).format(draft.route.deadlineMs) : "checking";
            const endpoints = (["buy", "sell"] as const).filter(endpoint => draft![endpoint] !== undefined && (endpoint === "buy" || draft!.complete));
            const chipYs = orderMarkerChipYs(endpoints.map(endpoint => facade?.priceToCoordinate(draft![endpoint]!) ?? -10000), paneHeight);
            for (const side of ["buy", "sell"] as const) {
                const line = rootRef.current.querySelector<HTMLElement>(`[data-risk-${side}]`);
                const chip = rootRef.current.querySelector<HTMLElement>(`[data-risk-chip='${side}']`);
                const color = (side === "buy") !== isShort ? up : down;
                const price = draft[side], y = !endpoints.includes(side) || price === undefined ? null : facade?.priceToCoordinate(price);
                const lineY = side === endpoint && previewVisible ? candidateY : y;
                if (line) {
                    line.style.display = lineY == null || lineY < 0 || lineY >= paneHeight ? "none" : "block";
                    line.style.top = `${lineY}px`;
                    line.style.right = `${facade?.priceScaleWidth() ?? 60}px`;
                    line.style.borderColor = color;
                }
                if (chip) {
                    chip.style.color = color;
                    chip.style.borderColor = color;
                    chip.style.display = y == null ? "none" : "flex";
                    chip.style.top = `${chipYs[endpoints.indexOf(side)]}px`;
                    const button = chip.querySelector<HTMLButtonElement>("[data-risk-price]");
                    if (button && price !== undefined) {
                        button.textContent = pxText(price);
                        const limit = side === "buy" ? s?.buyLimit : s?.sellLimit;
                        button.title = `${draft.complete ? `${qty.toLocaleString("en-US")} planned shares` : "Choose both prices to size shares"}\n${leg(side)} STOP-LIMIT · limit ${limit?.toFixed(limit < 1 ? 4 : 2) ?? "pending"}\nHeld by eTape · DAY ${deadline} · fees/execution risk excluded\n${draft.busy || draft.unknown ? "" : "Enter send · "}Esc cancel`;
                        if (draft.template.perpetual)
                            button.title = side === "buy" ? `${leg(side)} entry · Last trade triggers a limit using ${short() ? "bid minus" : "ask plus"} cushion · 24 hours` :
                                `${leg(side)} protective stop · Fair price · ${bingx() ? "Whole position · Held by eTape" : `Confirmed fills${isLivePerpetual ? " · Held by eTape" : ""}`}`;
                        button.setAttribute("aria-label", `${draft.template.perpetual || isShort ? leg(side) : side === "buy" ? "Buy" : "Sell"} trigger ${pxText(price)}; drag or use arrow keys to adjust`);
                        button.disabled = draft.busy || draft.unknown;
                    }
                    const cancel = chip.querySelector<HTMLButtonElement>("[data-risk-cancel]");
                    if (cancel) cancel.disabled = draft.busy;
                }
            }
            const label = rootRef.current.querySelector<HTMLElement>("[data-risk-preview-price]");
            if (label) {
                label.style.display = previewVisible ? "block" : "none";
                if (previewVisible && candidate != null) {
                    label.textContent = pxText(candidate);
                    label.style.top = `${Math.max(10.5, Math.min(candidateY!, paneHeight - 10.5))}px`;
                    label.style.left = `calc(100% - ${facade?.priceScaleWidth() ?? 60}px)`;
                    label.style.background = (endpoint === "buy") !== isShort ? up : down;
                    label.style.maxWidth = `${facade?.priceScaleWidth() ?? 60}px`;
                }
            }
            message(draft.template.auto && (draft.template.perpetual || canDrawStockShort()) ? (draft.buy === undefined ? "Click entry, then stop, or drag. Esc cancel." :
                !draft.complete ? "Stop below for long or above for short. Esc cancel." : "") :
                draft.template.perpetual ? (draft.buy === undefined ? `Click ${leg("buy")} entry, then stop; or drag. Esc cancel.` :
                !draft.complete ? `Choose the stop ${short() ? "above" : "below"} the entry. Esc cancel.` : "") :
                draft.buy === undefined ? "Click BUY, then SELL; or drag. Esc cancel." : !draft.complete ? "Choose SELL below BUY. Esc cancel." : "");
            cursor(previewVisible ? endpoint : null);
            if (previewVisible && endpoint && candidate != null) {
                host.dataset.orderCursorPrice = String(candidate);
                host.dataset.orderCursorY = String(candidateY);
                const limit = endpoint === "buy" ? s?.buyLimit : s?.sellLimit;
                host.title = `${draft.complete ? `${qty.toLocaleString("en-US")} planned shares` : "Choose both prices to size shares"}\n${leg(endpoint)} STOP-LIMIT · trigger ${candidate} · limit ${limit ?? "pending"}\nHeld by eTape · DAY ${deadline}\n${venue} · fees/execution risk excluded\nEsc cancel`;
                if (draft.template.perpetual)
                    host.title = endpoint === "buy" ? `${leg(endpoint)} entry · Last trade ${candidate} · Limit uses ${short() ? "bid minus" : "ask plus"} cushion` :
                        `${leg(endpoint)} protective stop · Fair price ${candidate}`;
            }
            const status = rootRef.current.querySelector<HTMLElement>("[data-risk-status]");
            if (status) {
                const route = draft.route, age = route?.lastEligibleTsMs ? Date.now() - route.lastEligibleTsMs : Infinity;
                const willTrigger = route?.hasTrustedEligiblePrint && age >= 0 && age <= 2000 && draft.buy !== undefined && route.lastEligiblePrice !== undefined && (isShort ? route.lastEligiblePrice <= draft.buy : route.lastEligiblePrice >= draft.buy);
                status.textContent = draft.unknown ? "Submit outcome unknown. Verify Orders; this setup cannot be resent." : draft.busy ? "Submitting linked pair…" :
                    [error, draft.error !== error ? draft.error : "", willTrigger ? `${isShort ? "SHORT" : "BUY"} WILL TRIGGER NOW` : "",
                        bingx() ? `${venue} ${isLivePerpetual ? "LIVE" : "VST"} · eTape-held whole-position stop` :
                            isLivePerpetual ? `${venue} LIVE · eTape-held fair-price stop` : ""].filter(Boolean).join(" · ");
                status.style.display = status.textContent ? "block" : "none";
                status.style.color = draft.busy ? latest.current.chrome.text : latest.current.chrome.down;
            }
            pillLayer.current?.sync();
            pillLayer.current?.drag(pointer?.editing ? rootRef.current.querySelector<HTMLElement>(`[data-risk-chip='${pointer.endpoint}']`) : null);
        };
        const schedule = () => { if (frame === null)
            frame = requestAnimationFrame(() => { frame = null; paint(); }); };
        const warmOpening = async () => {
            if (disposed || warming || !props.active || !props.configLoaded || !hasRisk || !needsOpening()) return;
            warming = true;
            const requestedQty = positionQty();
            const terms = await latest.current.sendQuery("QueryPerpetualOpening", { venue, symbol: props.symbol })
                .catch(() => undefined) as PerpetualOpeningPreview | undefined;
            warming = false;
            if (disposed) return;
            opening = terms?.long && terms.short ? terms : undefined;
            if (draft?.complete && !draft.maxQty) draft.maxQty = size(false)?.qty ?? 0;
            paint();
            if (bingx() && positionQty() !== requestedQty) void warmOpening();
        };
        const submit = async () => {
            if (!draft?.complete || draft.busy || draft.unknown || draft.buy === undefined || draft.sell === undefined)
                return;
            const current = draft;
            const symbol = latest.current.symbol;
            const notify = (reason: string, unknown = false) => {
                const detail = unknown ? "status unknown. Verify Orders; do not resubmit." + (reason ? ` ${reason}` : "") : reason;
                push({ level: unknown ? "warn" : "danger", text: `${symbol} (${venue}) Chart Risk Entry ${detail}` });
            };
            // Refresh custody/session at submit; eligible trades govern activation.
            current.busy = true;
            paint();
            try {
                if (!current.template.perpetual)
                    current.route = await latest.current.sendQuery("QueryStopLimitRoute", { tif: "DAY", session: "EXTENDED", symbol, riskEntry: true }) as StopLimitRoutePreview;
            }
            catch {
                notify("not sent: Engine preview unavailable.");
                if (draft === current) {
                    current.busy = false;
                    current.error = "Engine preview unavailable.";
                    paint();
                }
                return;
            }
            if (draft !== current)
                return;
            const p = latest.current;
            if (!p.linkGroups.matchesOrderContext(p.group, symbol, venue, p.stores.exec.status())) {
                clear();
                return;
            }
            delete current.error;
            const error = invalid();
            const s = size();
            if (error || !s || !s.qty) {
                current.busy = false;
                current.error = error || (current.template.perpetual ? "Zero contracts." : "Zero shares.");
                notify(`not sent: ${current.error}`);
                paint();
                return;
            }
            const context = protection(), effective = riskEntryAddTemplate(current.template, context.owner, context.stop);
            const args: SubmitRiskEntryArgs = { venue, symbol, ...(short() ? {side: "SHORT" as const} : {}), buyStop: current.buy!, sellStop: current.sell!, mode: current.template.mode, value: current.template.value,
                ...(current.sharedEntryId ? { sharedEntryId: current.sharedEntryId } : {}),
                maxQty: Math.min(current.maxQty, s.qty), buyCushion: current.template.buyCushion, sellCushion: effective.sellCushion,
                ...(effective.protection?{protection:effective.protection}:{}) };
            try {
                const ack = s.kind === "perpetual" ? await latest.current.sendCommand("SubmitPerpetualRiskEntry", {
                    venue, symbol, side: s.side, trigger: current.buy, stop: current.sell, leverage: s.leverage,
                    ...(s.autoLeverage ? { autoLeverage: true } : {}),
                    ...(bingx() && current.sharedEntryId ? { sharedStopId: current.sharedEntryId } : {}),
                    cushion: current.template.buyCushion, budget: current.template.value, maxQty: Math.min(current.maxQty, s.qty),
                    collateralPct: current.template.mode !== "Dollar",
                }) : await latest.current.sendCommand("SubmitRiskEntry", args);
                if (ack.ambiguous) notify(ack.reason || "", true);
                else if (ack.status !== "accepted") notify(`rejected: ${ack.reason || "Pair rejected."}`);
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
                    current.error = ack.reason || "Pair rejected.";
                    paint();
                }
            }
            catch (error) {
                notify(error instanceof Error ? error.message : "", true);
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
            let template = detail.template;
            const isPerpetual = p.symbol.startsWith("PERP.");
            if (!template.auto && isPerpetual !== !!template.perpetual) {
                push({ level: "warn", text: "Choose a Chart Risk Entry preset for the chart's instrument type." });
                return;
            }
            if (isPerpetual && !p.symbol.startsWith("PERP.MEXC.") && !bingx()) {
                push({ level: "warn", text: "Select a MEXC or BingX Chart Risk Entry venue for this contract." });
                return;
            }
            if (template.auto) {
                template = isPerpetual
                    ? { ...template, perpetual: { side: "BUY", ...template.auto }, protection: undefined }
                    : { ...template, perpetual: undefined };
            }
            clear();
            hover = lastPoint;
            draft = { template, sharedEntryId: protection().owner?.id ?? "", complete: false, busy: false, unknown: false, maxQty: 0 };
            host.dataset.riskEntryActive = "true";
            p.facadeRef.current?.setPanZoomEnabled?.(false);
            paint();
            const current = draft;
            if (current.template.perpetual) return;
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
            if (draft?.template.auto && draft.template.perpetual && !contract()) return null;
            const rect = host.getBoundingClientRect(), facade = latest.current.facadeRef.current, x = e.clientX - rect.left, y = e.clientY - rect.top;
            if (!facade || x < 0 || y < 0 || x >= rect.width - facade.priceScaleWidth() || y >= (facade.paneHeights()[0] ?? 0))
                return null;
            const price = facade.coordinateToPrice(y);
            return price != null && Number.isFinite(price) && price > 0 ? snap(price) : null;
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
            const pickingAutoStop = draft.template.auto && !draft.complete && draft.buy !== undefined;
            const hits = chip || pickingAutoStop ? [] : nearestPriceLines(selected, e.clientY - host.getBoundingClientRect().top, p => facade?.priceToCoordinate(p) ?? null);
            if (hits.length > 1) {
                consume(e);
                const chooser = rootRef.current?.querySelector<HTMLElement>("[data-risk-chooser]");
                if (chooser) {
                    for (const endpoint of ["buy", "sell"] as const) {
                        const choice = chooser.querySelector(`[data-risk-choice='${endpoint}']`);
                        if (choice) choice.textContent = leg(endpoint);
                        if (choice) choice.textContent = leg(endpoint);
                    }
                    chooser.style.display = "flex";
                    chooser.style.top = `${e.clientY - host.getBoundingClientRect().top}px`;
                }
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
            lastPoint = e;
            if (!draft || draft.busy || draft.unknown)
                return;
            if (pointer && e.pointerId !== pointer.id)
                return;
            hover = e;
            if (!pointer && (e.target as Element)?.closest?.("[data-drawing-ui],button,input,select,textarea")) { hover = null; cursor(null); schedule(); return; }
            const facade = latest.current.facadeRef.current;
            const raw = pointer?.axis ? facade?.coordinateToPrice((facade.priceToCoordinate(pointer.price) ?? pointer.y - host.getBoundingClientRect().top) + e.clientY - pointer.y) : priceAt(e);
            const price = raw != null && Number.isFinite(raw) && raw > 0 ? snap(raw) : null;
            if (price == null) {
                if (draft.template.auto && !draft.complete && !pointer?.editing) delete draft.sell;
                cursor(null);
                schedule();
                return;
            }
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
            if (!pointer && (!draft.template.auto || draft.complete)) {
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
            const corrected = pointer.editing && pointer.moved && draft.complete && draft[pointer.endpoint] !== pointer.price;
            if (completed && draft.template.auto) {
                hover = e;
                const stop = priceAt(e);
                if (stop === null) delete draft.sell;
                else {
                    draft.sell = stop;
                    draft.sellX = e.clientX - host.getBoundingClientRect().left;
                }
            }
            if (pointer.editing) hover = null;
            pointer = null;
            if (completed && draft.sell !== undefined && (!draft.template.auto || draft.buy !== draft.sell)) {
                const side = perpetualSide();
                if (draft.template.auto && draft.template.perpetual && side)
                    draft.template.perpetual.side = side;
                draft.stockShort = !draft.template.perpetual && short();
                draft.complete = true;
            }
            draft.maxQty = size(false)?.qty ?? 0;
            paint();
            if ((completed || corrected) && draft.complete && latest.current.config.chartRiskAutoSend)
                void submit();
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
                    const tick = draft.template.perpetual ? Number(contract()?.priceTick) : price >= 1 ? 0.01 : 0.0001;
                    const next = snap(price + (e.key === "ArrowUp" ? 1 : -1) * tick);
                    if (next > 0) draft[endpoint] = next;
                    draft.maxQty = size(false)?.qty ?? 0;
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
        let observedPositionQty = positionQty();
        const unsubscribe = latest.current.stores.exec.subscribe(() => {
            if (bingx()) {
                const qty = positionQty();
                if (qty !== observedPositionQty) {
                    observedPositionQty = qty;
                    void warmOpening();
                }
            }
            schedule();
        });
        const unsubscribeTrails = latest.current.stores.exec.subscribeTrails(schedule);
        const timer = setInterval(() => { if (draft)
            paint(); }, 1000);
        queueMicrotask(() => { void warmOpening(); });
        const openingTimer = setInterval(() => { void warmOpening(); }, 3000);
        return () => {
            disposed = true;
            clear();
            if (props.layoutRef.current === paint) props.layoutRef.current = () => {};
            if (frame !== null)
                cancelAnimationFrame(frame);
            clearInterval(timer);
            clearInterval(openingTimer);
            resize.disconnect();
            unsubscribe();
            unsubscribeTrails();
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
    }, [props.active, props.group, props.symbol, props.contextKey, props.activeTool, props.config.templates, props.configLoaded, props.layoutRef, venue, hasRisk, push]);
    return <div ref={rootRef} data-testid="chart-risk-entry" style={{ display: "none", position: "absolute", inset: 0, zIndex: 10, pointerEvents: "none" }}>
    <div data-order-line data-risk-buy style={{ position: "absolute", left: 0, borderTop: `2px dashed ${props.chrome.up}` }}/>
    <div data-order-line data-risk-sell style={{ position: "absolute", left: 0, borderTop: `2px dashed ${props.chrome.down}` }}/>
    <div data-risk-preview-price aria-hidden="true" style={{ display: "none", position: "absolute", transform: "translateY(-50%)", height: 21,
      boxSizing: "border-box", padding: "0 6px 0 10px", borderRadius: "0 2px 2px 0", color: "#fff",
      font: `12px/21px ${TV_FONT}`, textAlign: "left", whiteSpace: "nowrap", overflow: "hidden", pointerEvents: "none" }} />
    <svg data-risk-arrow aria-hidden="true" style={{ display: "none", position: "absolute", left: 0, top: 0, overflow: "hidden", pointerEvents: "none" }}>
      <path fill="none" stroke={props.chrome.down} strokeWidth={1.5} />
    </svg>
    <div data-risk-readout style={{ display: "none", position: "absolute", color: props.chrome.text, fontFamily: TV_FONT, fontSize: 12, lineHeight: "16px", textShadow: `0 0 2px ${props.chrome.bg}, 0 0 4px ${props.chrome.bg}`, pointerEvents: "none", overflowWrap: "anywhere" }} />
    <div data-risk-chooser role="dialog" aria-label="Choose draft price" style={{ display:"none",position:"absolute",right:70,pointerEvents:"auto",background:props.chrome.bg,border:`1px solid ${props.chrome.muted}`,padding:4,gap:4 }}>
      <button type="button" data-risk-choice="buy">BUY</button><button type="button" data-risk-choice="sell">SELL</button>
    </div>
    {(["buy", "sell"] as const).map(endpoint => <div key={endpoint} data-risk-chip={endpoint} data-drawing-ui style={{ display:"none",opacity:0,position:"absolute",right:0,transform:"translateY(-50%)",height:20,boxSizing:"border-box",border:`1px solid ${endpoint === "buy" ? props.chrome.up : props.chrome.down}`,borderRadius:3,background:"#0c1017",color:endpoint === "buy" ? props.chrome.up : props.chrome.down,pointerEvents:"auto",font:"600 10px ui-monospace,monospace",whiteSpace:"nowrap" }}>
      <button type="button" data-risk-price={endpoint} style={{border:0,background:"transparent",color:"inherit",font:"inherit",padding:"0 5px",cursor:"ns-resize"}} />
      <button type="button" data-risk-cancel aria-label="Discard risk setup" title="Discard both draft prices" style={{border:0,borderLeft:"1px solid currentColor",background:"transparent",color:"inherit",font:"bold 12px system-ui",padding:"0 5px",cursor:"pointer"}}>×</button>
    </div>)}
    <div data-risk-bar aria-live="polite" style={{ position: "absolute", left: 44, transform: "translateY(-100%)", color: props.chrome.text, fontFamily: TV_FONT, fontSize: 11, lineHeight: "14px", textShadow: `0 0 2px ${props.chrome.bg}, 0 0 4px ${props.chrome.bg}`, pointerEvents: "none", overflowWrap: "anywhere" }}>
      <div data-risk-detail style={{ whiteSpace: "pre-wrap" }}/>
      <div data-risk-status />
    </div>
  </div>;
}
