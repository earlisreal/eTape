// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { AckMsg, Order } from "../../../wire/contract";
import type { ChartApiFacade } from "../../../render/chart/ChartApiFacade";
import { ChartOrderMarkers } from "./ChartOrderMarkers";

const base: Order = {
  venue:"sim", id:"o1", symbol:"US.AAPL", side:"BUY", type:"LIMIT", tif:"DAY", session:"AUTO",
  qty:10, limitPrice:100, stopPrice:0, status:"ACCEPTED", executedQty:0, leavesQty:10,
  avgFillPrice:0, rejectReason:"", replacesId:"", createdMs:1, updatedMs:1,
};

function mount(orders: Order[] = [base]) {
  const host = document.createElement("div");
  host.getBoundingClientRect = () => ({ x:0, y:0, top:0, left:0, right:500, bottom:400, width:500, height:400, toJSON:() => ({}) });
  const hostRef = { current:host };
  const facadeRef = { current:{ priceToCoordinate:(price:number) => 300-price, coordinateToPrice:(y:number) => 300-y } as ChartApiFacade };
  const layoutRef = { current:() => {} };
  const chooserOpenRef = { current:false };
  const sendCommand = vi.fn(async ():Promise<AckMsg> => ({ kind:"ack", corrId:"c1", status:"accepted" }));
  document.body.append(host);
  const utils = render(<ChartOrderMarkers orders={orders} venue="sim" symbol="US.AAPL" pinned={false}
    sendCommand={sendCommand} hostRef={hostRef} facadeRef={facadeRef} rightAxisWidth={60}
    layoutRef={layoutRef} chooserOpenRef={chooserOpenRef} />, { container:host });
  return { ...utils, host, sendCommand };
}

beforeEach(() => { cleanup(); document.body.replaceChildren(); });

describe("ChartOrderMarkers", () => {
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
});
