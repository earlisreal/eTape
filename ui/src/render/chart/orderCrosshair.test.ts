// @vitest-environment jsdom
import { expect, it, vi } from "vitest";
import type { IChartApi } from "lightweight-charts";
import { installOrderCrosshair } from "./orderCrosshair";

it("snaps native mouse Y and price labels, preserves future-space X and restores normal formatting", () => {
  const host = document.createElement("div");
  host.innerHTML = '<div class="tv-lightweight-charts"><canvas></canvas></div>';
  host.getBoundingClientRect = () => ({left:0,top:0,width:500,height:400}) as DOMRect;
  const canvas = host.querySelector("canvas")!;
  const applyOptions = vi.fn();
  const chart = {applyOptions,options:() => ({crosshair:{horzLine:{labelBackgroundColor:"grey"}},localization:{}})} as unknown as IChartApi;
  let raw = 5.123;
  const cursor = installOrderCrosshair(chart,host,{coordinateToPrice:() => raw,priceToCoordinate:price => price*10,
    priceScaleWidth:() => 60,paneHeights:() => [300]},() => "grey");
  const received: {x:number;y:number}[] = [];
  canvas.addEventListener("mousemove",event => received.push({x:event.clientX,y:event.clientY}));
  canvas.dispatchEvent(new MouseEvent("mousemove",{bubbles:true,clientX:430,clientY:51.23}));
  cursor.set("green");
  expect(received.at(-1)).toEqual({x:430,y:51.2});
  const format = applyOptions.mock.calls.at(-1)![0].localization.priceFormatter;
  expect(format(5.123)).toBe("5.12");
  expect(format(0.51234)).toBe("0.5123");
  raw = 0.51234;
  host.dispatchEvent(new MouseEvent("mousemove",{bubbles:true,clientX:430,clientY:20,buttons:1}));
  expect(received.at(-1)!.x).toBe(430);
  expect(received.at(-1)!.y).toBeCloseTo(5.123,10);
  expect(Number(host.dataset.orderCursorPrice)).toBeCloseTo(0.5123,10);
  cursor.set(null);
  expect(applyOptions.mock.calls.at(-1)![0].localization.priceFormatter).toBeUndefined();
  expect(applyOptions.mock.calls.at(-1)![0].crosshair.horzLine.color).toBe("grey");
  canvas.dispatchEvent(new MouseEvent("mousemove",{bubbles:true,clientX:430,clientY:20}));
  expect(received.at(-1)).toEqual({x:430,y:20});
  cursor.dispose();
});

it.each(["risk", "gesture"])("hides only horizontal native feedback for %s and forwards captured pointer moves", mode => {
  const host = document.createElement("div");
  host.innerHTML = '<div class="tv-lightweight-charts"><canvas></canvas></div>';
  host.getBoundingClientRect = () => ({left:0,top:0,width:500,height:400}) as DOMRect;
  const canvas = host.querySelector("canvas")!;
  const applyOptions = vi.fn();
  const chart = {applyOptions,options:() => ({crosshair:{horzLine:{visible:true,labelBackgroundColor:"grey"}},localization:{}})} as unknown as IChartApi;
  let theme = "grey";
  const cursor = installOrderCrosshair(chart,host,{coordinateToPrice:y => 12-y/100,priceToCoordinate:p => (12-p)*100,
    priceScaleWidth:() => 60,paneHeights:() => [300]},() => theme);
  const received: {x:number;y:number}[] = [];
  canvas.addEventListener("mousemove",event => received.push({x:event.clientX,y:event.clientY}));
  canvas.dispatchEvent(new MouseEvent("mousemove",{bubbles:true,clientX:200,clientY:100}));
  host.dataset.orderEntryMode = mode;
  const pointer = new MouseEvent("pointermove",{clientX:430,clientY:240,buttons:1});
  window.dispatchEvent(pointer);
  cursor.set("red",pointer);
  expect(received.at(-1)).toEqual({x:430,y:240});
  expect(applyOptions.mock.calls.at(-1)![0].crosshair).toMatchObject({
    horzLine:{visible:false,labelVisible:false},vertLine:{color:"grey"},
  });
  expect(applyOptions.mock.calls.at(-1)![0].localization.priceFormatter).toBeUndefined();
  cursor.setHorizontalVisible(false);
  cursor.setHorizontalVisible(true);
  expect(applyOptions.mock.calls.at(-1)![0].crosshair.horzLine.visible).toBe(false);
  theme = "new theme";
  cursor.refresh();
  expect(applyOptions.mock.calls.at(-1)![0].crosshair.vertLine.color).toBe(theme);
  host.dataset.orderEntryMode = "order-drag";
  cursor.set("red");
  expect(applyOptions.mock.calls.at(-1)![0].crosshair).toMatchObject({horzLine:{visible:true,labelVisible:true},vertLine:{color:"red"}});
  cursor.set(null);
  expect(applyOptions.mock.calls.at(-1)![0].crosshair.vertLine.color).toBe(theme);
  cursor.setHorizontalVisible(false);
  host.dataset.orderEntryMode = mode;
  cursor.set("green");
  cursor.set(null);
  expect(applyOptions.mock.calls.at(-1)![0].crosshair.horzLine.visible).toBe(false);
  cursor.dispose();
});
