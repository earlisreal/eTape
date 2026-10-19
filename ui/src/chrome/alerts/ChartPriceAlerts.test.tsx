// @vitest-environment jsdom
import { render,screen,fireEvent,waitFor,cleanup } from "@testing-library/react";
import { expect,it,vi,afterEach } from "vitest";
import { ChartPriceAlerts } from "./ChartPriceAlerts";
import { PriceAlertStore } from "../../data/PriceAlertStore";
import type { PriceAlert } from "../../wire/contract";
afterEach(()=>{cleanup();document.body.replaceChildren();});
it("a chart tag opens the precise editor and Cancel never changes the alert",async()=>{
 const store=new PriceAlertStore();
 const a:PriceAlert={id:"a",version:1,symbol:"US.TEST",condition:"at-or-above",price:.5432,label:"Breakout",sessions:{pre:true,rth:true,post:true},state:"monitoring",createdMs:1,updatedMs:1};
 store.apply({kind:"snapshot",topic:"alerts.state",payload:{alerts:[a],history:[],historyTotal:0,revision:1,error:""}});
 const host=document.createElement("div");document.body.append(host);
 const sendCommand=vi.fn(async(name:string,args:unknown)=>{void name;void args;return {kind:"ack" as const,corrId:"c",status:"accepted" as const};});
 render(<ChartPriceAlerts store={store} commands={{sendCommand}} symbol="US.TEST" hostRef={{current:host}} facadeRef={{current:{priceToCoordinate:()=>100,coordinateToPrice:()=>.54,setPanZoomEnabled:()=>{}}}} layoutRef={{current:()=>{}}} rightAxisWidth={60} paneHeight={400} enabled={true} onEdit={()=>{}}/>,{container:host});
 fireEvent.click(screen.getByRole("button",{name:/Edit price alert/}));
 await waitFor(()=>expect(screen.getByLabelText<HTMLInputElement>("Target price").value).toBe("0.5432"));
 fireEvent.click(screen.getByRole("button",{name:"Cancel"}));
 expect(sendCommand.mock.calls.filter(c=>c[0]==="UpsertPriceAlert")).toHaveLength(0);
});

it("dragging previews a target and requires Save before sending a mutation",async()=>{
 const store=new PriceAlertStore();
 const a:PriceAlert={id:"a",version:1,symbol:"US.TEST",condition:"at-or-above",price:.5432,label:"Breakout",sessions:{pre:true,rth:true,post:true},state:"monitoring",createdMs:1,updatedMs:1};
 store.apply({kind:"snapshot",topic:"alerts.state",payload:{alerts:[a],history:[],historyTotal:0,revision:1,error:""}});
 const host=document.createElement("div");document.body.append(host);
 const sendCommand=vi.fn(async(name:string,args:unknown)=>{void name;void args;return {kind:"ack" as const,corrId:"c",status:"accepted" as const};});
 const pan=vi.fn();
 render(<ChartPriceAlerts store={store} commands={{sendCommand}} symbol="US.TEST" hostRef={{current:host}} facadeRef={{current:{priceToCoordinate:()=>100,coordinateToPrice:()=>.54,setPanZoomEnabled:pan}}} layoutRef={{current:()=>{}}} rightAxisWidth={60} paneHeight={400} enabled={true}/>,{container:host});
 fireEvent(screen.getByRole("button",{name:/Edit price alert/}),new MouseEvent("pointerdown",{bubbles:true,button:0,clientY:120}));
 fireEvent(window,new MouseEvent("pointermove",{bubbles:true,clientY:100}));
 fireEvent(window,new MouseEvent("pointerup",{bubbles:true,clientY:100}));
 await waitFor(()=>expect(screen.getByLabelText<HTMLInputElement>("Target price").value).toBe("0.54"));
 expect(sendCommand.mock.calls.filter(c=>c[0]==="UpsertPriceAlert")).toHaveLength(0);
 expect(pan).toHaveBeenLastCalledWith(true);
 fireEvent.click(screen.getByRole("button",{name:"Save alert"}));
 await waitFor(()=>expect(sendCommand).toHaveBeenCalledWith("UpsertPriceAlert",expect.objectContaining({id:"a",version:1,price:.54})));
});

it("bell creation preserves the established three-decimal dollar precision",async()=>{
 const store=new PriceAlertStore(),host=document.createElement("div");document.body.append(host);
 host.getBoundingClientRect=()=>({x:0,y:0,top:0,left:0,right:500,bottom:400,width:500,height:400,toJSON:()=>({})});
 const sendCommand=vi.fn(async()=>({kind:"ack" as const,corrId:"c",status:"accepted" as const}));
 render(<ChartPriceAlerts store={store} commands={{sendCommand}} symbol="US.TEST" hostRef={{current:host}} facadeRef={{current:{priceToCoordinate:()=>100,coordinateToPrice:()=>5.555,setPanZoomEnabled:()=>{}}}} layoutRef={{current:()=>{}}} rightAxisWidth={60} paneHeight={400} enabled={true} armed={true}/>,{container:host});
 fireEvent(host,new MouseEvent("pointerdown",{bubbles:true,button:0,clientX:100,clientY:100}));
 await waitFor(()=>expect(screen.getByLabelText<HTMLInputElement>("Target price").value).toBe("5.555"));
});
