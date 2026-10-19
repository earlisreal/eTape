// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { AckMsg, Order } from "../../../wire/contract";
import type { ChartApiFacade } from "../../../render/chart/ChartApiFacade";
import { ChartOrderMarkers } from "./ChartOrderMarkers";
import { getTvChrome } from "../../../render/chart/tvTheme";
import type { OrderConfig } from "../../exec/actionTemplate";
import { ChartPriceAlerts } from "../../alerts/ChartPriceAlerts";
import { PriceAlertStore } from "../../../data/PriceAlertStore";

const base: Order = {
  venue:"sim", id:"o1", symbol:"US.AAPL", side:"BUY", type:"LIMIT", tif:"DAY", session:"AUTO",
  qty:10, limitPrice:100, stopPrice:0, status:"ACCEPTED", executedQty:0, leavesQty:10,
  avgFillPrice:0, rejectReason:"", replacesId:"", createdMs:1, updatedMs:1,
};

function mount(orders: Order[] = [base], config?: OrderConfig, withAlert=false) {
  const host = document.createElement("div");
  host.getBoundingClientRect = () => ({ x:0, y:0, top:0, left:0, right:500, bottom:400, width:500, height:400, toJSON:() => ({}) });
  const hostRef = { current:host };
  const facadeRef = { current:{ priceToCoordinate:(price:number) => 300-price, coordinateToPrice:(y:number) => 300-y } as ChartApiFacade };
  const layoutRef = { current:() => {} };
  const chooserOpenRef = { current:false };
  const sendCommand = vi.fn(async ():Promise<AckMsg> => ({ kind:"ack", corrId:"c1", status:"accepted" }));
  document.body.append(host);
  const alertStore=new PriceAlertStore();
  alertStore.apply({kind:"snapshot",topic:"alerts.state",payload:{alerts:[{id:"a",symbol:"US.AAPL",price:100,state:"monitoring",condition:"at-or-above",label:"",sessions:{pre:true,rth:true,post:true},version:1,createdMs:1,updatedMs:1}],history:[],historyTotal:0,revision:1,error:""}});
  const utils = render(<><ChartOrderMarkers chrome={getTvChrome("light")} orders={orders} venue="sim" symbol="US.AAPL" pinned={false}
    availableCash={100000} buyingPower={100000}
    sendCommand={sendCommand} hostRef={hostRef} facadeRef={facadeRef} rightAxisWidth={60}
    layoutRef={layoutRef} chooserOpenRef={chooserOpenRef} {...(config ? {config} : {})} />
    {withAlert&&<ChartPriceAlerts store={alertStore} commands={{sendCommand}} symbol="US.AAPL" hostRef={hostRef} facadeRef={facadeRef} layoutRef={{current:()=>{}}} rightAxisWidth={60} paneHeight={400} enabled={true}/>}</>, { container:host });
  return { ...utils, host, sendCommand, layoutRef };
}

beforeEach(() => { cleanup(); document.body.replaceChildren(); vi.spyOn(document, "hasFocus").mockReturnValue(true); });

describe("ChartOrderMarkers", () => {
  it("keeps an order graphic above an alert at the same price",async()=>{
    const {host}=mount([base],undefined,true);
    await waitFor(()=>expect(host.querySelectorAll("[data-order-pill-visual]").length).toBeGreaterThan(1));
    const order=host.querySelector<HTMLElement>("[data-order-pill-visual='o1']")!;
    const alert=[...host.querySelectorAll<HTMLElement>("[data-order-pill-visual]")].find(v=>v!==order&&v.textContent?.includes("100"))!;
    expect(order.compareDocumentPosition(alert)&Node.DOCUMENT_POSITION_PRECEDING).not.toBe(0);
    fireEvent.pointerOver(screen.getByRole("button",{name:/Edit price alert/}));
    expect(alert.style.zIndex).toBe("1");
  });
  it("keeps the market-data waiting reason in hover details without a chart label", () => {
    const {host} = mount([{...base,type:"STOP_LIMIT",stopPrice:100,held:{phase:"WAITING",deadlineMs:9999999999999,pausedReason:"Waiting for market data"},
      riskEntry:{stopId:"s",budget:100,mode:"Dollar",buyCushion:{value:0,unit:"$"},sellCushion:{value:0,unit:"$"}}}]);
    expect(host.querySelector("[data-order-status]")?.textContent).toBe("");
    expect(host.querySelector<HTMLElement>("[data-order-status]")?.style.display).toBe("none");
    expect(host.querySelector<HTMLButtonElement>("[data-order-price]")?.title).toContain("Waiting for market data");
  });
  it("keeps resting pill graphics behind cursor labels without duplicating interactive controls", async () => {
    const {host} = mount();
    await waitFor(() => expect(host.querySelector("[data-order-pill-visual]")?.textContent).toBe("100.00×"));
    const visual = host.querySelector<HTMLElement>("[data-order-pill-visual]")!;
    expect(visual.style.zIndex).toBe("1");
    expect(visual.getAttribute("aria-hidden")).toBe("true");
    expect(visual.hasAttribute("inert")).toBe(true);
    expect(visual.style.pointerEvents).toBe("none");
    expect(screen.getAllByRole("button", {name:"Cancel BUY 10 LIMIT"})).toHaveLength(1);
    fireEvent.pointerOver(screen.getByTestId("order-label-o1"));
    expect(visual.style.zIndex).toBe("11");
    fireEvent.pointerOut(screen.getByTestId("order-label-o1"), {relatedTarget:host});
    expect(visual.style.zIndex).toBe("1");
  });
  it("does not show historical protection warnings over charts", () => {
    mount([{ ...base, status:"FILLED", executedQty:3, leavesQty:0,
      riskEntry:{stopId:"closed-stop",budget:10,mode:"Dollar",buyCushion:{value:0,unit:"$"},sellCushion:{value:0,unit:"$"},failure:"protection interrupted"} }]);
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByText(/shares remain/)).toBeNull();
  });
  it("clips horizontal lines under overlapping pills and clears the clip when they move away", async () => {
    const {host,layoutRef} = mount();
    await waitFor(() => expect(host.querySelector("[data-order-pill-visual]")).not.toBeNull());
    const visual = host.querySelector<HTMLElement>("[data-order-pill-visual]")!;
    const line = host.querySelector<HTMLElement>("[data-order-line]:not([data-order-confirmed-line])")!;
    visual.getBoundingClientRect = () => new DOMRect(440,190,60,20);
    line.getBoundingClientRect = () => new DOMRect(0,200,450,2);
    layoutRef.current();
    expect(line.style.clipPath).toBe("inset(0 10px 0 0)");
    visual.getBoundingClientRect = () => new DOMRect(440,220,60,20);
    layoutRef.current();
    expect(line.style.clipPath).toBe("inset(0 0px 0 0)");
  });
  it("gives drag priority over hover and focus, then restores focus or resting priority and disposes graphics", () => {
    const {host,unmount} = mount([base,{...base,id:"sell",side:"SELL",limitPrice:105}]);
    const buy = screen.getByTestId("order-label-o1"), sell = screen.getByTestId("order-label-sell");
    const front = () => [...host.querySelectorAll<HTMLElement>("[data-order-pill-visual]")]
      .filter(el => el.style.zIndex === "11").map(el => el.dataset.orderPillVisual);
    buy.focus();
    expect(front()).toEqual(["o1"]);
    fireEvent.pointerOver(sell);
    expect(front()).toEqual(["sell"]);
    fireEvent.pointerDown(buy,{button:0,pointerId:1,clientX:20,clientY:200});
    fireEvent.pointerOver(sell);
    expect(front()).toEqual(["o1"]);
    fireEvent.keyDown(buy,{key:"Tab"});
    fireEvent.pointerUp(window,{pointerId:1,clientX:20,clientY:200});
    expect(front()).toEqual(["o1"]);
    buy.blur();
    expect(front()).toEqual([]);
    fireEvent.pointerDown(host,{button:0,pointerId:2,clientX:100,clientY:200});
    expect(front()).toEqual(["o1"]);
    fireEvent.pointerCancel(window,{pointerId:2});
    expect(front()).toEqual([]);
    unmount();
    expect(host.querySelector("[data-order-pill-layer]")).toBeNull();
  });
  it("drags the nearest plot line through the existing replace path and ignores other pointers", async () => {
    const {host,sendCommand} = mount([base,{...base,id:"near",side:"SELL",limitPrice:105}]);
    fireEvent.pointerMove(host,{pointerId:1,clientX:100,clientY:194});
    expect(host.title).toContain("SELL LIMIT");
    expect(host.style.cursor).toBe("ns-resize");
    fireEvent.pointerDown(host,{button:0,pointerId:1,clientX:100,clientY:194});
    fireEvent.pointerMove(window,{pointerId:2,clientX:100,clientY:180});
    fireEvent.pointerUp(window,{pointerId:2});
    expect(sendCommand).not.toHaveBeenCalled();
    fireEvent.pointerMove(window,{pointerId:1,clientX:100,clientY:184});
    expect(screen.getByTestId("order-label-near").textContent).toBe("115.00");
    expect(host.querySelector("[data-order-pill-visual='near']")?.textContent).toBe("115.00×");
    fireEvent.pointerUp(window,{pointerId:1});
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("ReplaceOrder",expect.objectContaining({orderId:"near",limitPrice:115})));
    expect(sendCommand).toHaveBeenCalledTimes(1);
  });
  it("offers a cross-side chooser for exact line ties", () => {
    const {host,sendCommand} = mount([base,{...base,id:"sell",side:"SELL"}]);
    fireEvent.pointerDown(host,{button:0,pointerId:1,clientX:100,clientY:200});
    expect(screen.getByRole("dialog",{name:"Choose chart order"}).textContent).toContain("SELL");
    expect(sendCommand).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button",{name:"Close order chooser"}));
    fireEvent.pointerDown(host,{button:0,pointerId:1,clientX:100,clientY:190});
    expect(sendCommand).not.toHaveBeenCalled();
  });
  it("lets a bound gesture win over line grabbing and cancels ordinary line changes", () => {
    const config: OrderConfig = {activeVenue:"sim",templates:[{kind:"place",id:"stop",label:"Stop",side:"SELL",type:"STOP_LIMIT",tif:"DAY",session:"EXTENDED",priceSource:"Last",priceOffset:0,
      chartBinding:"Shift",limitCushion:0,limitCushionUnit:"$",sizing:{mode:"Shares",shares:1}}]};
    const {host,sendCommand} = mount([base],config);
    fireEvent.pointerDown(host,{button:0,pointerId:1,clientX:100,clientY:200,shiftKey:true});
    fireEvent.pointerMove(window,{pointerId:1,clientX:100,clientY:190,shiftKey:true});
    fireEvent.pointerUp(window,{pointerId:1});
    expect(sendCommand).not.toHaveBeenCalled();
    fireEvent.pointerDown(host,{button:0,pointerId:1,clientX:100,clientY:200});
    fireEvent.pointerMove(window,{pointerId:1,clientX:100,clientY:190});
    expect(host.querySelector("[data-order-pill-visual='o1']")?.textContent).toBe("110.00×");
    fireEvent.keyDown(window,{key:"Escape"});
    fireEvent.pointerUp(window,{pointerId:1});
    expect(sendCommand).not.toHaveBeenCalled();
    expect(screen.getByTestId("order-label-o1").textContent).toBe("100.00");
    expect(host.querySelector("[data-order-pill-visual='o1']")?.textContent).toBe("100.00×");
  });
  it.each(["BUY", "COVER", "SELL", "SHORT"] as const)("keeps sub-dollar precision and semantic side for %s", async (side) => {
    const {host} = mount([{...base,side,limitPrice:0.1234}]);
    const price = screen.getByTestId("order-label-o1");
    expect(price.textContent).toBe("0.1234");
    expect(price.getAttribute("aria-label")).toContain(side);
    expect(price.title).toContain(`${side} LIMIT`);
    await waitFor(() => expect(host.querySelector("[data-order-pill-visual='o1']")?.textContent).toBe("0.1234×"));
  });
  it("uses compact side-colored axis chips, shares tooltips, and a same-side chooser", async () => {
    const { host, sendCommand } = mount([base, {...base,id:"o2"}, {...base,id:"sell",side:"SELL"}]);
    const groups = host.querySelectorAll<HTMLElement>("[data-order-group]");
    expect(groups).toHaveLength(2);
    expect(groups[0].style.right).toBe("0px");
    expect(groups[0].style.getPropertyValue("--order-color")).toBe(getTvChrome("light").up);
    expect(groups[1].style.getPropertyValue("--order-color")).toBe(getTvChrome("light").down);
    const buy = screen.getByTestId("order-label-o1");
    expect(buy.textContent).toBe("100.00 (2)");
    expect(buy.title).toContain("10 shares\nBUY LIMIT · limit 100.00");
    expect(screen.getByTestId("order-label-sell").textContent).toBe("100.00");
    expect(host.querySelector("[data-order-risk]")).toBeNull();
    fireEvent.click(buy);
    fireEvent.click(screen.getAllByRole("button", {name:"Cancel BUY 10 LIMIT"})[1]);
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("CancelOrder", {venue:"sim",orderId:"o2"}));
  });
  it("keeps keyboard price adjustment on the axis chip", async () => {
    const {sendCommand} = mount();
    fireEvent.keyDown(screen.getByTestId("order-label-o1"), {key:"ArrowUp"});
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("ReplaceOrder", expect.objectContaining({qty:0,limitPrice:100.01})));
  });
  it("does not pull offscreen order chips into the visible price pane", () => {
    const {host} = mount([{...base,id:"offscreen",limitPrice:500},base]);
    expect(host.querySelector<HTMLElement>("[data-order-ids='offscreen']")?.style.display).toBe("none");
    expect(host.querySelector<HTMLElement>("[data-order-ids='o1']")?.style.display).toBe("block");
    expect(host.querySelector<HTMLElement>("[data-order-ids='o1'] [data-order-chip]")?.style.top).toBe("-10px");
  });
  it("sends the observed phase when dragging a linked trigger",async()=>{
    const order:Order={...base,type:"STOP_LIMIT",stopPrice:100,limitPrice:100.05,held:{phase:"WAITING",deadlineMs:9999999999999},
      riskEntry:{stopId:"s",budget:100,mode:"Dollar",buyCushion:{value:0.05,unit:"$"},sellCushion:{value:0,unit:"$"}}};
    const stop:Order={...order,id:"s",side:"SELL",qty:0,leavesQty:0,stopPrice:98,limitPrice:98,riskEntryId:"o1"};
    delete stop.riskEntry;
    const {sendCommand}=mount([order,stop]);
    expect(screen.getByTestId("order-label-s").title).toContain("10 planned shares");
    expect(screen.getByTestId("order-label-s").title).toContain("Held by eTape");
    fireEvent.pointerDown(screen.getByTestId("order-label-o1"),{button:0,pointerId:1,clientX:20,clientY:200});
    fireEvent.pointerMove(window,{pointerId:1,clientX:20,clientY:190});
    expect(screen.getByTestId("order-label-o1").title).toContain("8 planned shares");
    expect(screen.getByTestId("order-label-o1").title).not.toContain("estimated risk");
    fireEvent.pointerUp(window,{pointerId:1,clientX:20,clientY:190});
    await waitFor(()=>expect(sendCommand).toHaveBeenCalledWith("ReplaceOrder",expect.objectContaining({stopPrice:110,qty:8,expectedHeldPhase:"WAITING",expectedRiskEntryPhase:"WAITING"})));
  });
  it("previews the snapped price during drag and sends a price-only replace on release", async () => {
    const { sendCommand } = mount();
    const label = screen.getByTestId("order-label-o1");
    fireEvent.pointerDown(label, { button:0, pointerId:1, clientX:20, clientY:200 });
    fireEvent.pointerMove(window, { pointerId:1, clientX:20, clientY:189 });
    expect(screen.getByTestId("chart-order-markers").querySelector("[data-order-group]")?.getAttribute("data-proposed-price")).toBe("111");
    fireEvent.pointerUp(window, { pointerId:1, clientX:20, clientY:189 });
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("ReplaceOrder", {
      venue:"sim", orderId:"o1", qty:0, limitPrice:111, stopPrice:0,
    }));
    expect(document.querySelector<HTMLElement>("[data-order-pill-visual='o1']")?.style.zIndex).toBe("1");
  });

  it("cancels a drag on Escape without sending a replace", () => {
    const { sendCommand } = mount();
    fireEvent.pointerDown(screen.getByTestId("order-label-o1"), { button:0, pointerId:1, clientX:20, clientY:200 });
    fireEvent.pointerMove(window, { pointerId:1, clientX:20, clientY:189 });
    fireEvent.keyDown(window, { key:"Escape" });
    fireEvent.pointerUp(window, { pointerId:1, clientX:20, clientY:189 });
    expect(sendCommand).not.toHaveBeenCalled();
    expect(screen.getByText("Price change canceled.")).toBeTruthy();
  });

  it("cancels the selected order from the price-axis X", async () => {
    const { sendCommand } = mount();
    fireEvent.click(screen.getByRole("button", { name:"Cancel BUY 10 LIMIT" }));
    await waitFor(() => expect(sendCommand).toHaveBeenCalledWith("CancelOrder", { venue:"sim", orderId:"o1" }));
  });

  it("keeps an absolute chart overlay mounted with no working orders", () => {
    const { host } = mount([]);
    const overlay = host.querySelector<HTMLElement>("[data-testid='chart-order-markers']");
    const liveRegion = overlay?.querySelector<HTMLElement>(".chart-order-announcement");
    expect(overlay?.style.position).toBe("absolute");
    expect(liveRegion?.getAttribute("aria-live")).toBe("polite");
    expect(liveRegion?.style.clipPath).toBe("inset(50%)");
  });
});
