// @vitest-environment jsdom
import { beforeEach, it, expect, vi } from "vitest";
import { render, fireEvent, cleanup, waitFor, screen } from "@testing-library/react";
import { makeStores } from "../../../data/registry";
import { LinkGroups } from "../../linkGroups";
import type { ChartApiFacade } from "../../../render/chart/ChartApiFacade";
import { getTvChrome } from "../../../render/chart/tvTheme";
import { initiateChartRiskEntry, type RiskEntryTemplate } from "../../exec/actionTemplate";
import { ChartRiskEntry } from "./ChartRiskEntry";
const template: RiskEntryTemplate = { kind: "risk", id: "r", label: "Risk", mode: "Dollar", value: 100, buyCushion: { value: 0, unit: "$" }, sellCushion: { value: 0, unit: "$" } };
function mount(auto = false, preset = template) {
    const stores = makeStores();
    stores.exec.apply({ kind: "snapshot", topic: "exec.status", payload: { masterArmed: true, venues: [{ venue: "sim", broker: "sim", connected: true, positionDataReady: true }] } });
    stores.exec.apply({ kind: "snapshot", topic: "exec.account", payload: { venue: "sim", buyingPower: 100000, availableCash: 100000, tsMs: Date.now() } });
    const linkGroups = new LinkGroups({ post: () => { }, onMessage: () => () => { }, close: () => { } }, () => { });
    linkGroups.focusVenue("green", "sim");
    const host = document.createElement("div");
    document.body.append(host);
    host.getBoundingClientRect = () => ({ x: 0, y: 0, top: 0, left: 0, right: 500, bottom: 400, width: 500, height: 400, toJSON: () => ({}) });
    const facadeRef = { current: { setOrderCrosshair: vi.fn(), priceScaleWidth: () => 60, paneHeights: () => [400], coordinateToPrice: (y: number) => 12 - y / 100, priceToCoordinate: (p: number) => (12 - p) * 100 } as unknown as ChartApiFacade };
    const sendCommand = vi.fn(async (name: string, args: unknown) => { void name; void args; return { kind: "ack" as const, corrId: "1", status: "accepted" as const, orderId: "E1" }; });
    const sendQuery = vi.fn(async () => ({ route: "ENGINE_HELD", effectiveSession: "EXTENDED", phase: "RTH", deadlineMs: Date.now() + 100000, hasTrustedEligiblePrint: true, lastEligiblePrice: 9, lastEligibleTsMs: Date.now() }));
    const layoutRef = { current: () => {} };
    const element = (symbol = "AAPL") => <ChartRiskEntry chrome={getTvChrome("light")} panelId="chart" active group="green" symbol={symbol} contextKey="1m" hostRef={{ current: host }} facadeRef={facadeRef} stores={stores} linkGroups={linkGroups} config={{ activeVenue: "sim", templates: [preset], chartRiskAutoSend: auto }} configLoaded activeTool="select" chooserOpenRef={{ current: false }} layoutRef={layoutRef} sendCommand={sendCommand} sendQuery={sendQuery}/>;
    const result = render(element(), { container: host });
    sendCommand.mockClear();
    return { ...result, host, sendCommand, sendQuery, facadeRef, layoutRef, stores, changeSymbol: (symbol: string) => result.rerender(element(symbol)) };
}
let frames: Map<number, FrameRequestCallback>;
function flushFrames() {
    const pending = [...frames.values()];
    frames.clear();
    pending.forEach(cb => cb(0));
}
function placeRiskPair(host: HTMLElement, gesture: "drag" | "click" = "drag") {
    fireEvent.pointerDown(host, { pointerId: 1, button: 0, clientX: 100, clientY: 200 });
    if (gesture === "click") {
        fireEvent.pointerUp(window, { pointerId: 1 });
        fireEvent.pointerDown(host, { pointerId: 2, button: 0, clientX: 100, clientY: 220 });
        fireEvent.pointerUp(window, { pointerId: 2 });
    } else {
        fireEvent.pointerMove(window, { pointerId: 1, clientX: 100, clientY: 220 });
        fireEvent.pointerUp(window, { pointerId: 1 });
    }
}
beforeEach(() => {
    cleanup();
    document.body.replaceChildren();
    vi.restoreAllMocks();
    vi.spyOn(document, "hasFocus").mockReturnValue(true);
    frames = new Map();
    let frameId = 0;
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => { frames.set(++frameId, cb); return frameId; });
    vi.stubGlobal("cancelAnimationFrame", (id: number) => frames.delete(id));
    vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
});
it("colors selection, hides the unselected SELL preview, and edits the first BUY without completing or sending", async () => {
    const {host,sendCommand,facadeRef} = mount(true);
    initiateChartRiskEntry(template);
    expect(facadeRef.current.setOrderCrosshair).toHaveBeenLastCalledWith(getTvChrome("light").up);
    fireEvent.pointerDown(host,{pointerId:1,button:0,clientX:100,clientY:200});
    fireEvent.pointerUp(window,{pointerId:1});
    fireEvent.pointerMove(host,{pointerId:1,clientX:100,clientY:220});
    flushFrames();
    expect(facadeRef.current.setOrderCrosshair).toHaveBeenLastCalledWith(getTvChrome("light").down);
    expect(host.querySelector<HTMLElement>("[data-risk-sell]")?.style.display).toBe("none");
    expect(host.querySelector<HTMLElement>("[data-risk-chip='sell']")?.style.display).toBe("none");
    fireEvent.pointerDown(host,{pointerId:2,button:0,clientX:100,clientY:200});
    fireEvent.pointerMove(window,{pointerId:2,clientX:100,clientY:190});
    fireEvent.pointerUp(window,{pointerId:2});
    expect(host.querySelector("[data-risk-price='buy']")?.textContent).toBe("B 10.10");
    expect(host.querySelector<HTMLElement>("[data-risk-chip='sell']")?.style.display).toBe("none");
    fireEvent.keyDown(window,{key:"Enter"});
    expect(sendCommand).not.toHaveBeenCalledWith("SubmitRiskEntry",expect.anything());
    fireEvent.keyDown(window,{key:"Escape"});
    expect(facadeRef.current.setOrderCrosshair).toHaveBeenLastCalledWith(null);
});
it("previews two clicks and sends one risk-sized pair on Enter", async () => {
    const { host, sendCommand } = mount();
    expect(initiateChartRiskEntry(template)).toBe(true);
    fireEvent.pointerDown(host, { pointerId: 1, button: 0, clientX: 100, clientY: 200 });
    fireEvent.pointerUp(window, { pointerId: 1, button: 0, clientX: 100, clientY: 200 });
    fireEvent.pointerDown(host, { pointerId: 2, button: 0, clientX: 100, clientY: 220 });
    fireEvent.pointerUp(window, { pointerId: 2, button: 0, clientX: 100, clientY: 220 });
    const buy = host.querySelector<HTMLButtonElement>("[data-risk-price='buy']")!;
    await waitFor(() => expect(buy.title).toContain("500 planned shares"));
    expect(buy.textContent).toBe("B 10.00");
    expect(buy.title).toContain("Enter send · Esc cancel");
    expect(buy.title).not.toContain("$100.00");
    expect(host.querySelector("[data-risk-detail]")?.textContent).toBe("");
    expect(screen.getByTestId("chart-risk-entry").textContent).not.toContain("notional");
    expect(host.querySelector("[data-risk-readout]")?.textContent).toBe("500 shares · Est. risk $100.00");
    expect(sendCommand).not.toHaveBeenCalledWith("SubmitRiskEntry", expect.anything());
    fireEvent.keyDown(window, { key: "Enter" });
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("SubmitRiskEntry", expect.objectContaining({ buyStop: 10, sellStop: 9.8, maxQty: 500, value: 100, mode: "Dollar" })));
    fireEvent.keyDown(window, { key: "Enter" });
    expect(sendCommand.mock.calls.filter(([name]) => name === "SubmitRiskEntry")).toHaveLength(1);
});
it("updates the provisional arrow and sizing on successive mouse frames, hides outside the pane, and relayouts without a mouse move", () => {
    const {host,sendCommand,facadeRef,layoutRef} = mount();
    initiateChartRiskEntry(template);
    fireEvent.pointerDown(host,{pointerId:1,button:0,clientX:100,clientY:200});
    fireEvent.pointerUp(window,{pointerId:1});
    const arrow = host.querySelector<SVGSVGElement>("[data-risk-arrow]")!;
    const readout = host.querySelector<HTMLElement>("[data-risk-readout]")!;
    for (const [x,y,text] of [[200,220,"500 shares · Est. risk $100.00"], [300,230,"333 shares · Est. risk $99.90"]] as const) {
        fireEvent.pointerMove(host,{pointerId:1,clientX:x,clientY:y});
        flushFrames();
        expect(readout.textContent).toBe(text);
        const path = arrow.querySelector("path")!.getAttribute("d")!.split(" ");
        expect(Number(path[1])).toBe((100+x)/2);
        expect(Number(path[2])).toBeCloseTo(200);
        expect(Number(path[4])).toBeCloseTo(y);
    }
    fireEvent.pointerMove(window,{pointerId:1,clientX:450,clientY:230});
    flushFrames();
    expect(arrow.style.display).toBe("none");
    expect(readout.style.display).toBe("none");
    fireEvent.pointerMove(host,{pointerId:1,clientX:300,clientY:230});
    flushFrames();
    expect(arrow.style.display).toBe("block");
    facadeRef.current.priceToCoordinate = p => (12-p)*100 + 10;
    layoutRef.current();
    const path = arrow.querySelector("path")!.getAttribute("d")!.split(" ");
    expect(Number(path[1])).toBe(200);
    expect(Number(path[2])).toBeCloseTo(210);
    expect(Number(path[4])).toBeCloseTo(240);
    expect(sendCommand).not.toHaveBeenCalledWith("SubmitRiskEntry", expect.anything());
    fireEvent.keyDown(window,{key:"Escape"});
    expect(screen.getByTestId("chart-risk-entry").style.display).toBe("none");
});
it("uses cushions and funding caps, preserves the completed quantity cap, and sends the displayed quantity", async () => {
    const preset: RiskEntryTemplate = {...template, mode:"CashPct",value:10,buyCushion:{value:0.1,unit:"$"},sellCushion:{value:1,unit:"%"}};
    const {host,stores,sendCommand,layoutRef} = mount(false,preset);
    const account = (cash: number, tsMs = Date.now()) => stores.exec.apply({kind:"snapshot",topic:"exec.account",payload:{venue:"sim",availableCash:cash,buyingPower:cash,tsMs}});
    account(1000);
    initiateChartRiskEntry(preset);
    fireEvent.pointerDown(host,{pointerId:1,button:0,clientX:100,clientY:200});
    fireEvent.pointerUp(window,{pointerId:1});
    fireEvent.pointerMove(host,{pointerId:1,clientX:200,clientY:220});
    flushFrames();
    const readout = host.querySelector<HTMLElement>("[data-risk-readout]")!;
    expect(readout.textContent).toBe("99 shares · Est. risk $39.60");
    fireEvent.pointerDown(host,{pointerId:2,button:0,clientX:200,clientY:220});
    fireEvent.pointerUp(window,{pointerId:2});
    account(2000); flushFrames();
    expect(readout.textContent).toBe("99 shares · Est. risk $39.60");
    account(500); flushFrames();
    expect(readout.textContent).toBe("49 shares · Est. risk $19.60");
    account(2000,Date.now()-31000); layoutRef.current();
    expect(readout.textContent).toBe("— shares · Est. risk —");
    expect(host.querySelector("[data-risk-status]")?.textContent).toContain("Fresh account data required");
    account(2000); flushFrames();
    fireEvent.keyDown(window,{key:"Enter"});
    await waitFor(()=>expect(sendCommand).toHaveBeenCalledWith("SubmitRiskEntry",expect.objectContaining({maxQty:99})));
});
it("shows blocked sizing while selecting an invalid SELL or a zero-share setup", () => {
    const {host,stores} = mount();
    initiateChartRiskEntry(template);
    fireEvent.pointerDown(host,{pointerId:1,button:0,clientX:100,clientY:200});
    fireEvent.pointerUp(window,{pointerId:1});
    fireEvent.pointerMove(host,{pointerId:1,clientX:200,clientY:190}); flushFrames();
    expect(host.querySelector("[data-risk-readout]")?.textContent).toBe("— shares · Est. risk —");
    expect(host.querySelector("[data-risk-status]")?.textContent).toContain("Sell trigger must be below buy trigger");
    stores.exec.apply({kind:"snapshot",topic:"exec.account",payload:{venue:"sim",availableCash:1,buyingPower:1,tsMs:Date.now()}});
    fireEvent.pointerMove(host,{pointerId:1,clientX:200,clientY:220}); flushFrames();
    expect(host.querySelector("[data-risk-status]")?.textContent).toContain("zero shares");
});
it("auto-sends one drag on release and cancels another setup on Escape", async () => {
    const { host, sendCommand } = mount(true);
    initiateChartRiskEntry(template);
    await waitFor(() => expect(screen.getByTestId("chart-risk-entry").textContent).not.toContain("Checking engine"));
    fireEvent.pointerDown(host, { pointerId: 1, button: 0, clientX: 100, clientY: 200 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 100, clientY: 220 });
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 100, clientY: 220 });
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("SubmitRiskEntry", expect.objectContaining({ maxQty: 500 })));
    sendCommand.mockClear();
    initiateChartRiskEntry(template);
    fireEvent.pointerDown(host, { pointerId: 2, button: 0, clientX: 100, clientY: 200 });
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.getByTestId("chart-risk-entry").style.display).toBe("none");
    fireEvent.pointerMove(window, { pointerId: 2, clientX: 100, clientY: 220 });
    fireEvent.pointerUp(window, { pointerId: 2, clientX: 100, clientY: 220 });
    expect(sendCommand).not.toHaveBeenCalled();
});
it.each(["drag", "click"] as const)("refreshes an expired initial preview before auto-sending a %s setup", async gesture => {
    const clock = vi.spyOn(Date, "now").mockReturnValue(1_800_000_000_000);
    const { host, sendCommand, sendQuery } = mount(true);
    initiateChartRiskEntry(template);
    await waitFor(() => expect(host.querySelector("[data-risk-status]")?.textContent).toBe(""));
    clock.mockReturnValue(Date.now() + 2001);
    placeRiskPair(host, gesture);
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("SubmitRiskEntry", expect.objectContaining({ buyStop: 10, sellStop: 9.8, maxQty: 500 })));
    expect(sendQuery).toHaveBeenCalledTimes(2);
    expect(sendCommand.mock.calls.filter(([name]) => name === "SubmitRiskEntry")).toHaveLength(1);
    expect(screen.getByTestId("chart-risk-entry").style.display).toBe("none");
});
it("blocks a failed submission refresh even when the initial preview is fresh, and allows an explicit retry", async () => {
    vi.spyOn(Date, "now").mockReturnValue(1_800_000_000_000);
    const { host, sendCommand, sendQuery } = mount(true);
    initiateChartRiskEntry(template);
    await waitFor(() => expect(host.querySelector("[data-risk-status]")?.textContent).toBe(""));
    sendQuery.mockRejectedValueOnce(new Error("offline"));
    placeRiskPair(host);
    await waitFor(() => expect(host.querySelector("[data-risk-status]")?.textContent).toBe("Engine preview unavailable."));
    expect(sendCommand).not.toHaveBeenCalledWith("SubmitRiskEntry", expect.anything());
    expect(host.querySelector<HTMLButtonElement>("[data-risk-price='buy']")?.disabled).toBe(false);
    fireEvent.keyDown(window, { key: "Enter" });
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("SubmitRiskEntry", expect.anything()));
    expect(sendQuery).toHaveBeenCalledTimes(3);
    expect(sendCommand.mock.calls.filter(([name]) => name === "SubmitRiskEntry")).toHaveLength(1);
});
it.each(["untrusted", "stale"])("blocks %s refreshed market data without retrying automatically", async state => {
    vi.spyOn(Date, "now").mockReturnValue(1_800_000_000_000);
    const { host, sendCommand, sendQuery, stores } = mount(true);
    initiateChartRiskEntry(template);
    await waitFor(() => expect(host.querySelector("[data-risk-status]")?.textContent).toBe(""));
    const initial = await sendQuery.mock.results[0].value;
    sendQuery.mockResolvedValueOnce({ ...initial, hasTrustedEligiblePrint: state !== "untrusted", lastEligibleTsMs: state === "stale" ? Date.now() - 2001 : Date.now() });
    placeRiskPair(host);
    await waitFor(() => expect(host.querySelector("[data-risk-status]")?.textContent).toBe("Fresh eligible market data required."));
    stores.exec.apply({ kind: "snapshot", topic: "exec.account", payload: { venue: "sim", buyingPower: 100000, availableCash: 100000, tsMs: Date.now() } });
    flushFrames();
    expect(sendQuery).toHaveBeenCalledTimes(2);
    expect(sendCommand).not.toHaveBeenCalledWith("SubmitRiskEntry", expect.anything());
    expect(host.querySelector<HTMLButtonElement>("[data-risk-price='buy']")?.disabled).toBe(false);
});
it("clears an earlier preview error after a successful refresh", async () => {
    const { host, sendCommand, sendQuery, stores } = mount(true);
    sendQuery.mockRejectedValueOnce(new Error("offline"));
    initiateChartRiskEntry(template);
    await waitFor(() => expect(host.querySelector("[data-risk-status]")?.textContent).toBe("Engine preview unavailable."));
    stores.exec.apply({ kind: "snapshot", topic: "exec.status", payload: { masterArmed: false, venues: [{ venue: "sim", connected: true, positionDataReady: true }] } });
    placeRiskPair(host);
    await waitFor(() => expect(host.querySelector("[data-risk-status]")?.textContent).toBe("Trading is locked. Arm the engine first."));
    expect(sendQuery).toHaveBeenCalledTimes(2);
    expect(sendCommand).not.toHaveBeenCalledWith("SubmitRiskEntry", expect.anything());
});
it.each(["send", "escape", "blur", "symbol"])("keeps one pending refresh owned by its draft: %s", async outcome => {
    vi.spyOn(Date, "now").mockReturnValue(1_800_000_000_000);
    const { host, sendCommand, sendQuery, changeSymbol } = mount(true);
    initiateChartRiskEntry(template);
    await waitFor(() => expect(host.querySelector("[data-risk-status]")?.textContent).toBe(""));
    const initial = await sendQuery.mock.results[0].value;
    let resolve!: (preview: typeof initial) => void;
    const pending = new Promise<typeof initial>(done => { resolve = done; });
    sendQuery.mockReturnValueOnce(pending);
    placeRiskPair(host);
    fireEvent.keyDown(window, { key: "Enter" });
    expect(sendQuery).toHaveBeenCalledTimes(2);
    expect(sendCommand).not.toHaveBeenCalledWith("SubmitRiskEntry", expect.anything());
    if (outcome === "escape") fireEvent.keyDown(window, { key: "Escape" });
    if (outcome === "blur") fireEvent.blur(window);
    if (outcome === "symbol") changeSymbol("MSFT");
    resolve(initial);
    await pending;
    if (outcome === "send") {
        await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("SubmitRiskEntry", expect.anything()));
        expect(sendCommand.mock.calls.filter(([name]) => name === "SubmitRiskEntry")).toHaveLength(1);
    } else {
        expect(sendCommand).not.toHaveBeenCalledWith("SubmitRiskEntry", expect.anything());
    }
    expect(screen.getByTestId("chart-risk-entry").style.display).toBe("none");
});
it.each(["locked", "disconnected", "stale account"])("revalidates %s execution after refreshing", async blocker => {
    vi.spyOn(Date, "now").mockReturnValue(1_800_000_000_000);
    const { host, sendCommand, sendQuery, stores } = mount(true);
    initiateChartRiskEntry(template);
    await waitFor(() => expect(host.querySelector("[data-risk-status]")?.textContent).toBe(""));
    if (blocker === "stale account") {
        stores.exec.apply({ kind: "snapshot", topic: "exec.account", payload: { venue: "sim", buyingPower: 100000, availableCash: 100000, tsMs: Date.now() - 31000 } });
    } else {
        stores.exec.apply({ kind: "snapshot", topic: "exec.status", payload: { masterArmed: blocker !== "locked", venues: [{ venue: "sim", connected: blocker !== "disconnected", positionDataReady: true }] } });
    }
    placeRiskPair(host);
    const reason = blocker === "locked" ? "Trading is locked. Arm the engine first." : blocker === "disconnected" ? "Venue or position data unavailable; reconcile first." : "Fresh account data required.";
    await waitFor(() => expect(host.querySelector("[data-risk-status]")?.textContent).toBe(reason));
    expect(sendQuery).toHaveBeenCalledTimes(2);
    expect(sendCommand).not.toHaveBeenCalledWith("SubmitRiskEntry", expect.anything());
});
it("keeps blockers and uncertain submission outcomes visible", async () => {
    const { host, sendCommand, sendQuery } = mount();
    sendQuery.mockRejectedValueOnce(new Error("offline"));
    initiateChartRiskEntry(template);
    await waitFor(() => expect(host.querySelector("[data-risk-status]")?.textContent).toBe("Engine preview unavailable."));
    expect(screen.queryByRole("button")).toBeNull();
    fireEvent.keyDown(window, { key: "Escape" });

    sendCommand.mockImplementation(async () => ({ kind: "ack", corrId: "1", status: "accepted", orderId: "E1", ambiguous: true }));
    initiateChartRiskEntry(template);
    fireEvent.pointerDown(host, { pointerId: 1, button: 0, clientX: 100, clientY: 200 });
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 100, clientY: 200 });
    fireEvent.pointerDown(host, { pointerId: 2, button: 0, clientX: 100, clientY: 220 });
    fireEvent.pointerUp(window, { pointerId: 2, clientX: 100, clientY: 220 });
    fireEvent.keyDown(window, { key: "Enter" });
    await waitFor(() => expect(host.querySelector("[data-risk-status]")?.textContent).toContain("Submit outcome unknown"));
    expect(host.querySelector<HTMLButtonElement>("[data-risk-price='buy']")?.title).toContain("500 planned shares");
    expect(host.querySelector<HTMLButtonElement>("[data-risk-price='buy']")?.title).not.toContain("Enter send");
    fireEvent.keyDown(window, { key: "Enter" });
    expect(sendCommand.mock.calls.filter(([name]) => name === "SubmitRiskEntry")).toHaveLength(1);
});
it("drags a draft price on the axis and discards the pair from either X", async () => {
    const {host,sendCommand} = mount();
    initiateChartRiskEntry(template);
    fireEvent.pointerDown(host,{pointerId:1,button:0,clientX:100,clientY:200});
    fireEvent.pointerUp(window,{pointerId:1,clientX:100,clientY:200});
    fireEvent.pointerDown(host,{pointerId:2,button:0,clientX:100,clientY:220});
    fireEvent.pointerUp(window,{pointerId:2,clientX:100,clientY:220});
    const buy = host.querySelector<HTMLButtonElement>("[data-risk-price='buy']")!;
    fireEvent.pointerDown(buy,{pointerId:3,button:0,clientX:470,clientY:200});
    fireEvent.pointerMove(window,{pointerId:3,clientX:470,clientY:190});
    fireEvent.pointerUp(window,{pointerId:3,clientX:470,clientY:190});
    await waitFor(() => expect(buy.title).toContain("333 planned shares"));
    expect(buy.textContent).toBe("B 10.10");
    expect(host.querySelector("[data-risk-readout]")?.textContent).toBe("333 shares · Est. risk $99.90");
    fireEvent.keyDown(buy,{key:"ArrowUp"});
    expect(host.querySelector("[data-risk-readout]")?.textContent).toBe("322 shares · Est. risk $99.82");
    const cancel = screen.getAllByRole("button",{name:"Discard risk setup"})[1];
    fireEvent.keyDown(cancel,{key:"Enter"});
    expect(sendCommand).not.toHaveBeenCalledWith("SubmitRiskEntry",expect.anything());
    fireEvent.click(cancel);
    expect(screen.getByTestId("chart-risk-entry").style.display).toBe("none");
});
it("uses sub-dollar order ticks and outward cushion rounding in the visible estimate and wire command", async () => {
    const preset: RiskEntryTemplate = {...template,buyCushion:{value:0.00003,unit:"$"}};
    const {host,facadeRef,sendCommand} = mount(false,preset);
    facadeRef.current.coordinateToPrice = y => 0.99994 - (y-200)*0.00002;
    facadeRef.current.priceToCoordinate = price => 200 + (0.99994-price)/0.00002;
    initiateChartRiskEntry(preset);
    fireEvent.pointerDown(host,{pointerId:1,button:0,clientX:100,clientY:200});
    fireEvent.pointerUp(window,{pointerId:1});
    fireEvent.pointerMove(host,{pointerId:1,clientX:200,clientY:220}); flushFrames();
    expect(host.querySelector("[data-risk-readout]")?.textContent).toBe("100,000 shares · Est. risk $50.00");
    fireEvent.pointerDown(host,{pointerId:2,button:0,clientX:200,clientY:220});
    fireEvent.pointerUp(window,{pointerId:2});
    fireEvent.keyDown(window,{key:"Enter"});
    await waitFor(()=>expect(sendCommand).toHaveBeenCalledWith("SubmitRiskEntry",expect.objectContaining({buyStop:expect.closeTo(0.9999,4),sellStop:expect.closeTo(0.9995,4),maxQty:100000})));
});
