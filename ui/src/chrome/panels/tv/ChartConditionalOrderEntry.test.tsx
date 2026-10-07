// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { makeStores } from "../../../data/registry";
import type { ChartApiFacade } from "../../../render/chart/ChartApiFacade";
import type { ExecStatus, StopLimitRoutePreview } from "../../../wire/contract";
import { LinkGroups } from "../../linkGroups";
import type { OrderConfig } from "../../exec/actionTemplate";
import { ChartConditionalOrderEntry } from "./ChartConditionalOrderEntry";
import { getTvChrome } from "../../../render/chart/tvTheme";

const config = {
  activeVenue:"sim",
  templates:[{kind:"place", id:"stop", label:"Stop", side:"BUY", type:"STOP_LIMIT", tif:"DAY", session:"EXTENDED",
    priceSource:"Last", priceOffset:0, limitCushion:0, limitCushionUnit:"$", chartBinding:"Shift", sizing:{mode:"Shares", shares:1}}],
} satisfies OrderConfig;
const route: StopLimitRoutePreview = {route:"ENGINE_HELD", effectiveSession:"EXTENDED", deadlineMs:1_800_000_000_000, phase:"PRE",
  hasTrustedEligiblePrint:true, lastEligiblePrice:101};

function mount(env:"paper"|"live" = "paper", orderConfig: OrderConfig = config,
  routePreview: typeof route = route, positionDataReady = true) {
  const stores = makeStores();
  const status: ExecStatus = {masterArmed:true, global:{maxDayLoss:0,maxSymbolPositionValue:0,maxSymbolPositionShares:0}, venues:[{
    venue:"sim", broker:env === "paper" ? "sim" : "alpaca", env, connected:true, reconcilePending:false, positionDataReady, flattenPending:false, note:"", lastReconcileMs:null,
    gate:{maxOrderValue:10000,maxPositionValue:10000,maxPositionShares:100,maxOpenOrders:10}, heldStopLimitAcknowledged:false,
  }]};
  stores.exec.apply({kind:"snapshot",topic:"exec.status",payload:status});
  stores.exec.apply({kind:"snapshot",topic:"exec.account",key:"sim",payload:{venue:"sim",equity:10000,buyingPower:10000,availableCash:10000,sodEquity:10000,realized:0,dayPnl:0,leverage:1,tsMs:1,cycleStartMs:0,cycleRealized:0}});
  const linkGroups = new LinkGroups({post:() => {}, onMessage:() => () => {}, close:() => {}}, () => {});
  linkGroups.focusVenue("green", "sim");
  const host = document.createElement("div");
  host.getBoundingClientRect = () => ({x:0,y:0,top:0,left:0,right:500,bottom:400,width:500,height:400,toJSON:() => ({})});
  document.body.append(host);
  const hostRef = {current:host};
  const facadeRef = {current:{priceScaleWidth:() => 60, paneHeights:() => [400], coordinateToPrice:(y:number) => 120-y/10,
    priceToCoordinate:(price:number) => (120-price)*10} as ChartApiFacade};
  const chooserOpenRef = {current:false};
  const sendCommand = vi.fn(async (name:string) => ({kind:"ack" as const,corrId:"c1",status:"accepted" as const, ...(name === "SubmitOrder" ? {orderId:"ET1"} : {})}));
  const sendQuery = vi.fn(async () => routePreview);
  const utils = render(<ChartConditionalOrderEntry chrome={getTvChrome("light")} hostRef={hostRef} facadeRef={facadeRef} stores={stores} linkGroups={linkGroups}
    group="green" symbol="US.AAPL" config={orderConfig} configLoaded activeTool="select" chooserOpenRef={chooserOpenRef}
    sendCommand={sendCommand} sendQuery={sendQuery} />, {container:host});
  return { ...utils, host, stores, sendCommand, sendQuery, facadeRef };
}

function focusChart(): void { vi.spyOn(document, "hasFocus").mockReturnValue(true); }
function moveToChart(host:HTMLElement, shiftKey = true, ctrlKey = false): void {
  fireEvent.pointerMove(host, {pointerId:1,button:0,clientX:100,clientY:200,shiftKey,ctrlKey});
}
async function placeClick(host:HTMLElement): Promise<void> {
  fireEvent.pointerDown(host, {pointerId:1,button:0,clientX:100,clientY:200,shiftKey:true});
  fireEvent.pointerUp(window, {pointerId:1,button:0,clientX:101,clientY:201,shiftKey:true});
}

beforeEach(() => { cleanup(); document.body.replaceChildren(); vi.restoreAllMocks();
  vi.stubGlobal("requestAnimationFrame", (cb:FrameRequestCallback) => {cb(0);return 0;});
  vi.stubGlobal("cancelAnimationFrame", () => {});
});

describe("ChartConditionalOrderEntry", () => {
  it("previews a colored line and price label on modifier press and cancels until rearmed", async () => {
    focusChart();
    const {host,sendCommand,facadeRef} = mount("paper",config,{...route,lastEligiblePrice:99});
    const setColor = vi.fn();
    facadeRef.current.setOrderCrosshair = setColor;
    moveToChart(host, false);
    fireEvent.keyDown(window,{key:"Shift",shiftKey:true});
    const preview = screen.getByTestId("chart-order-entry-preview");
    await waitFor(() => expect(preview.style.opacity).toBe("1"));
    expect(preview.querySelector<HTMLElement>("[data-entry-line]")?.style.top).toBe("200px");
    expect(preview.querySelector<HTMLElement>("[data-entry-line]")?.style.right).toBe("60px");
    expect(preview.querySelector("[data-entry-preview-price]")?.textContent).toBe("100.00");
    expect(preview.querySelector<HTMLElement>("[data-entry-preview-price]")?.style.top).toBe("200px");
    expect(preview.querySelector("[data-entry-chip],button")).toBeNull();
    expect(host.title).toContain("1 shares\nBUY STOP-LIMIT · limit 100.00");
    expect(host.dataset.orderCursorPrice).toBe("100");
    expect(setColor).toHaveBeenLastCalledWith(getTvChrome("light").up, expect.any(MouseEvent));
    fireEvent.keyDown(window,{key:"Escape",shiftKey:true});
    expect(preview.style.visibility).toBe("hidden");
    expect(setColor).toHaveBeenLastCalledWith(null);
    await placeClick(host);
    expect(sendCommand).not.toHaveBeenCalledWith("SubmitOrder",expect.anything());
    fireEvent.keyUp(window,{key:"Shift"});
    await placeClick(host);
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("SubmitOrder",expect.anything()));
  });
  it.each([
    ["BUY", "STOP_LIMIT", 100.006, "100.01"],
    ["SELL", "STOP_LIMIT", 0.13364, "0.1336"],
    ["COVER", "LIMIT_IF_TOUCHED", 0.45004, "0.4500"],
    ["SHORT", "LIMIT_IF_TOUCHED", 100.004, "100.00"],
  ] as const)("shows the snapped %s %s preview price and hides it on release", async (side, type, price, text) => {
    focusChart();
    const {host,facadeRef,sendCommand} = mount("paper", {...config, templates:[{...config.templates[0], side, type}]});
    facadeRef.current.coordinateToPrice = () => price;
    facadeRef.current.priceToCoordinate = () => 200;
    moveToChart(host);
    const preview = screen.getByTestId("chart-order-entry-preview");
    await waitFor(() => expect(preview.querySelector("[data-entry-preview-price]")?.textContent).toBe(text));
    expect(preview.style.getPropertyValue("--entry-color")).toBe(side === "BUY" || side === "COVER" ? getTvChrome("light").up : getTvChrome("light").down);
    expect(sendCommand).not.toHaveBeenCalled();
    fireEvent.keyUp(window,{key:"Shift"});
    expect(preview.style.visibility).toBe("hidden");
  });
  it("keeps an uncertain submit outcome visible after hiding the gesture preview", async () => {
    focusChart();
    const {host,sendCommand} = mount();
    sendCommand.mockResolvedValueOnce({kind:"ack",corrId:"c1",status:"accepted",ambiguous:true} as Awaited<ReturnType<typeof sendCommand>>);
    await placeClick(host);
    await waitFor(() => expect(host.querySelector("[data-entry-status]")?.textContent).toContain("Order outcome unknown"));
    expect(host.querySelector<HTMLElement>("[data-entry-status]")?.style.display).toBe("block");
  });
  it("submits the first Shift-click without waiting for a hover preview", async () => {
    focusChart();
    const {host,sendCommand} = mount();
    await placeClick(host);
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("SubmitOrder", expect.objectContaining({
      type:"STOP_LIMIT",stopPrice:100,limitPrice:100,qty:1,routeExpected:"ENGINE_HELD",
    })));
  });

  it("submits one Shift-click after the hover route preview expires", async () => {
    focusChart();
    const {host,sendCommand} = mount();
    moveToChart(host);
    await waitFor(() => expect(screen.getByTestId("chart-order-entry-preview").textContent).toContain("WILL TRIGGER NOW"));
    const now = Date.now();
    vi.spyOn(Date, "now").mockReturnValue(now + 300);
    await placeClick(host);
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("SubmitOrder", expect.anything()));
    expect(sendCommand).toHaveBeenCalledTimes(1);
  });

  it("waits for a slow route query, preserves the clicked price, and submits once per modifier press", async () => {
    focusChart();
    const {host,sendCommand,sendQuery,facadeRef} = mount();
    let resolveRoute!: (preview: typeof route) => void;
    sendQuery.mockImplementationOnce(() => new Promise(resolve => { resolveRoute = resolve; }));
    await placeClick(host);
    await placeClick(host);
    expect(sendCommand).not.toHaveBeenCalled();
    facadeRef.current.coordinateToPrice = () => 80;
    await act(async () => { resolveRoute(route); });
    expect(sendCommand).toHaveBeenCalledWith("SubmitOrder", expect.objectContaining({stopPrice:100,limitPrice:100}));
    await placeClick(host);
    expect(sendCommand).toHaveBeenCalledTimes(1);
    fireEvent.keyUp(window, {key:"Shift"});
    await placeClick(host);
    await waitFor(() => expect(sendCommand).toHaveBeenCalledTimes(2));
  });

  it.each(["escape", "blur", "pointercancel", "leave", "unmount"])("does not submit after %s while the route query is pending", async (cancel) => {
    focusChart();
    const {host,sendCommand,sendQuery,unmount} = mount();
    let resolveRoute!: (preview: typeof route) => void;
    sendQuery.mockImplementationOnce(() => new Promise(resolve => { resolveRoute = resolve; }));
    await placeClick(host);
    if (cancel === "escape") fireEvent.keyDown(window, {key:"Escape"});
    if (cancel === "blur") fireEvent.blur(window);
    if (cancel === "pointercancel") fireEvent.pointerCancel(window);
    if (cancel === "leave") fireEvent.pointerLeave(host);
    if (cancel === "unmount") unmount();
    await act(async () => { resolveRoute(route); });
    expect(sendCommand).not.toHaveBeenCalled();
  });

  it("rechecks the trading lock after a pending route query", async () => {
    focusChart();
    const {host,sendCommand,sendQuery,stores} = mount();
    let resolveRoute!: (preview: typeof route) => void;
    sendQuery.mockImplementationOnce(() => new Promise(resolve => { resolveRoute = resolve; }));
    await placeClick(host);
    act(() => stores.exec.apply({kind:"delta",topic:"exec.status",payload:{...stores.exec.status()!,masterArmed:false}}));
    await act(async () => { resolveRoute(route); });
    expect(sendCommand).not.toHaveBeenCalled();
    expect(screen.getByTestId("chart-order-entry-preview").textContent).toContain("Trading is locked");
  });

  it("does not queue a cold click made while trading is locked", async () => {
    focusChart();
    const {host,sendCommand,sendQuery,stores} = mount();
    act(() => stores.exec.apply({kind:"delta",topic:"exec.status",payload:{...stores.exec.status()!,masterArmed:false}}));
    await placeClick(host);
    act(() => stores.exec.apply({kind:"delta",topic:"exec.status",payload:{...stores.exec.status()!,masterArmed:true}}));
    expect(sendQuery).not.toHaveBeenCalled();
    expect(sendCommand).not.toHaveBeenCalled();
  });

  it("blocks a cold live gesture until the held account is acknowledged", async () => {
    focusChart();
    const {host,sendCommand} = mount("live");
    await placeClick(host);
    await waitFor(() => expect(screen.getByTestId("chart-order-entry-preview").textContent).toContain("Review / enable live accounts"));
    expect(sendCommand).not.toHaveBeenCalled();
  });

  it("blocks and announces a failed route query", async () => {
    focusChart();
    const {host,sendCommand,sendQuery} = mount();
    sendQuery.mockRejectedValueOnce(new Error("Route query failed"));
    await placeClick(host);
    await waitFor(() => expect(screen.getByTestId("chart-order-entry-preview").textContent).toContain("Engine route preview unavailable"));
    expect(sendCommand).not.toHaveBeenCalled();
  });

  it("uses exact Shift+click as the stop trigger and submits the previewed route snapshot", async () => {
    focusChart();
    const {host,sendCommand,sendQuery} = mount();
    moveToChart(host);
    await waitFor(() => expect(sendQuery).toHaveBeenCalledWith("QueryStopLimitRoute", {tif:"DAY",session:"EXTENDED",symbol:"US.AAPL",deferredPositionSizing:false}));
    await waitFor(() => expect(screen.getByTestId("chart-order-entry-preview").textContent).toContain("WILL TRIGGER NOW"));
    await placeClick(host);
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("SubmitOrder", expect.objectContaining({
      venue:"sim",symbol:"US.AAPL",type:"STOP_LIMIT",stopPrice:100,limitPrice:100,qty:1,routeExpected:"ENGINE_HELD",
    })));
  });

  it("uses the inverse LIT trigger direction, includes the exact deadline, and says when equality will trigger", async () => {
    focusChart();
    const lit = { ...config.templates[0], type: "LIMIT_IF_TOUCHED" as const, limitCushion: 0.1 };
    const litConfig = { ...config, templates: [lit] };
    const { host, sendCommand, sendQuery } = mount("paper", litConfig, { ...route, lastEligiblePrice: 100 });
    moveToChart(host);
    await waitFor(() => expect(sendQuery).toHaveBeenCalledWith("QueryStopLimitRoute", {
      tif: "DAY", session: "EXTENDED", symbol: "US.AAPL", deferredPositionSizing: false, type: "LIMIT_IF_TOUCHED",
    }));
    await waitFor(() => expect(screen.getByTestId("chart-order-entry-preview").textContent).toContain("WILL TRIGGER NOW"));
    const tooltip = host.title;
    expect(tooltip).toContain("no broker order before activation");
    expect(tooltip).toContain("primary moomoo OpenD Last-Eligible Prints");
    expect(tooltip).toContain("feed loss pauses evaluation");
    await placeClick(host);
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("SubmitOrder", expect.objectContaining({
      venue: "sim", symbol: "US.AAPL", type: "LIMIT_IF_TOUCHED", stopPrice: 100, limitPrice: 100.1,
      qty: 1, routeExpected: "ENGINE_HELD", routeDeadlineMs: route.deadlineMs,
    })));
  });

  it("previews and submits a flat percentage stop-sell with immediate-hit feedback", async () => {
    focusChart();
    const template = { ...config.templates[0], side:"SELL" as const, sizing:{mode:"PositionFraction" as const,pct:100} };
    const deferredConfig = { ...config, templates:[template] };
    const {host,sendCommand,sendQuery} = mount("paper", deferredConfig, { ...route, lastEligiblePrice:99 });
    moveToChart(host);
    await waitFor(() => expect(sendQuery).toHaveBeenCalledWith("QueryStopLimitRoute", {
      tif:"DAY",session:"EXTENDED",symbol:"US.AAPL",deferredPositionSizing:true,
    }));
    const preview = screen.getByTestId("chart-order-entry-preview");
    await waitFor(() => expect(preview.textContent).toContain("WILL TRIGGER NOW — NO OPEN POSITION"));
    expect(host.title).toContain("100% position · shares determined on trigger");
    expect(preview.style.getPropertyValue("--entry-color")).toBe(getTvChrome("light").down);
    await placeClick(host);
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("SubmitOrder", expect.objectContaining({
      venue:"sim",symbol:"US.AAPL",side:"SELL",type:"STOP_LIMIT",qty:0,deferredPositionPct:100,routeExpected:"ENGINE_HELD",
    })));
  });

  it("does not label an untrusted flat cache as confirmed no-position", async () => {
    focusChart();
    const template = { ...config.templates[0], side:"SELL" as const, sizing:{mode:"PositionFraction" as const,pct:100} };
    const deferredConfig = { ...config, templates:[template] };
    const {host} = mount("paper", deferredConfig, { ...route, lastEligiblePrice:99 }, false);
    moveToChart(host);
    const preview = screen.getByTestId("chart-order-entry-preview");
    await waitFor(() => expect(preview.textContent).toContain("Position cache is reconciling"));
    expect(preview.textContent).not.toContain("WILL TRIGGER NOW — NO OPEN POSITION");
  });

  it("does not submit when extra modifiers are held", async () => {
    focusChart();
    const {host,sendCommand,sendQuery} = mount();
    moveToChart(host);
    await waitFor(() => expect(sendQuery).toHaveBeenCalled());
    fireEvent.pointerDown(host, {pointerId:1,button:0,clientX:100,clientY:200,shiftKey:true,ctrlKey:true});
    fireEvent.pointerUp(window, {pointerId:1,button:0,clientX:100,clientY:200,shiftKey:true,ctrlKey:true});
    expect(sendCommand).not.toHaveBeenCalledWith("SubmitOrder", expect.anything());
  });

  it("directs an unacknowledged live engine-held gesture to Settings without hiding its preview", async () => {
    focusChart();
    const {host,sendCommand,sendQuery,stores} = mount("live");
    moveToChart(host);
    await waitFor(() => expect(sendQuery).toHaveBeenCalled());
    const preview = screen.getByTestId("chart-order-entry-preview");
    await waitFor(() => expect(preview.style.opacity).toBe("1"));
    await placeClick(host);
    expect(screen.queryByRole("dialog", {name:"Live engine-held stop-limit disclosure"})).toBeNull();
    expect(preview.style.opacity).toBe("1");
    expect(host.title).toContain("LOCAL · eTape-held");
    expect(preview.querySelector("[data-entry-detail]")?.textContent).toContain("Settings → Orders & hotkeys → Review / enable live accounts");
    expect(preview.querySelector("[data-entry-announcement]")?.textContent).toContain("Settings → Orders & hotkeys → Review / enable live accounts");
    expect(sendCommand).not.toHaveBeenCalledWith("SubmitOrder", expect.anything());
    expect(sendCommand).not.toHaveBeenCalledWith("AcknowledgeHeldStopLimit", expect.anything());

    act(() => stores.exec.apply({ kind: "delta", topic: "exec.status", payload: {
      ...stores.exec.status()!,
      venues: stores.exec.status()!.venues.map((v) => ({ ...v, heldStopLimitAcknowledged: true })),
    } }));
    expect(sendCommand).not.toHaveBeenCalledWith("SubmitOrder", expect.anything());
    fireEvent.keyUp(window, { key: "Shift" });
    moveToChart(host);
    await placeClick(host);
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("SubmitOrder", expect.anything()));
  });
});
