// @vitest-environment jsdom
import { render,screen,fireEvent,cleanup,waitFor } from "@testing-library/react";
import { expect,it,vi,afterEach } from "vitest";
import { AlertsPanel } from "../panels/AlertsPanel";
import { makeStores } from "../../data/registry";
import type { PriceAlert,PriceAlertTrigger } from "../../wire/contract";
import { LinkGroups } from "../linkGroups";
import type { PanelConfig } from "../workspace";
afterEach(cleanup);
function setup() {
 const stores=makeStores();
 const alert:PriceAlert={id:"a",symbol:"US.TEST",sourceGroup:"yellow",condition:"at-or-above",price:5.5,label:"Breakout",sessions:{pre:true,rth:true,post:true},state:"monitoring",version:1,createdMs:1,updatedMs:1};
 const history:PriceAlertTrigger={id:"h",alertId:"deleted",symbol:"US.TEST",condition:"at-or-above",price:5.5,actualPrice:5.6,label:"Breakout",sessions:{pre:true,rth:true,post:true},session:"rth",tsMs:1,reason:"Target reached"};
 stores.priceAlerts.apply({kind:"snapshot",topic:"alerts.state",payload:{alerts:[alert],history:[history],historyTotal:1,revision:1,error:""}});
 const sendCommand=vi.fn(async()=>({kind:"ack" as const,corrId:"c",status:"accepted" as const}));
 const linkGroups=new LinkGroups({post:()=>{},onMessage:()=>()=>{},close:()=>{}},()=>{});
 const focus=vi.spyOn(linkGroups,"focus");
 const config:PanelConfig={id:"alerts",panelId:"alerts",group:"red",settings:{}};
 const props={stores,commands:{sendCommand,sendQuery:async()=>null},config,linkGroups,group:"blue" as const};
 return {props,focus,sendCommand};
}

it("loads only clicked symbols into the panel color in Alerts and deleted-definition History",()=>{
 const {props,focus,sendCommand}=setup();
 const oldNavigation=vi.fn();window.addEventListener("etape:alert-chart",oldNavigation);
 render(<AlertsPanel {...props}/>);
 fireEvent.click(screen.getByText("Breakout"));
 expect(document.querySelector("article")?.getAttribute("data-selected")).toBe("true");
 expect(focus).not.toHaveBeenCalled();
 const symbol=screen.getByRole("button",{name:"Load TEST into blue group"});
 expect(symbol.tagName).toBe("BUTTON");
 fireEvent.click(symbol);
 expect(focus).toHaveBeenCalledWith("blue","US.TEST");
 expect(sendCommand).not.toHaveBeenCalled();
 focus.mockClear();
 fireEvent.click(screen.getByRole("button",{name:"Pause"}));
 expect(focus).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("tab",{name:"History (1)"}));
 expect(screen.getByText("Alert deleted")).toBeTruthy();
 fireEvent.click(screen.getByRole("button",{name:"Load TEST into blue group"}));
 expect(focus).toHaveBeenCalledWith("blue","US.TEST");
 expect(screen.queryByRole("button",{name:"View chart"})).toBeNull();
 expect(oldNavigation).not.toHaveBeenCalled();
 window.removeEventListener("etape:alert-chart",oldNavigation);
});

it("uses live color changes and preserves explicit detachment without a source fallback",()=>{
 const {props,focus}=setup();
 const view=render(<AlertsPanel {...props}/>);
 view.rerender(<AlertsPanel {...props} group="green"/>);
 fireEvent.click(screen.getByRole("button",{name:"Load TEST into green group"}));
 expect(focus).toHaveBeenLastCalledWith("green","US.TEST");
 focus.mockClear();
 view.rerender(<AlertsPanel {...props} group={null}/>);
 const button=screen.getByRole<HTMLButtonElement>("button",{name:"Load TEST"});
 expect(button.disabled).toBe(true);fireEvent.click(button);
 expect(screen.getByText("Choose a Link Group color to load symbols.")).toBeTruthy();
 expect(focus).not.toHaveBeenCalled();
 view.rerender(<AlertsPanel stores={props.stores} commands={props.commands} config={props.config} linkGroups={props.linkGroups}/>);
 fireEvent.click(screen.getByRole("button",{name:"Load TEST into red group"}));
 expect(focus).toHaveBeenCalledWith("red","US.TEST");
});

it("captures the Alerts Panel's creation color",async()=>{
 const {props,sendCommand}=setup();render(<AlertsPanel {...props}/>);
 fireEvent.click(screen.getByRole("button",{name:"+ Alert"}));
 fireEvent.change(screen.getByLabelText("Symbol"),{target:{value:"TEST"}});
 fireEvent.change(screen.getByLabelText("Target price"),{target:{value:"5.5"}});
 fireEvent.click(screen.getByRole("button",{name:"Save alert"}));
 await waitFor(()=>expect(sendCommand).toHaveBeenCalledWith("UpsertPriceAlert",expect.objectContaining({sourceGroup:"blue",symbol:"US.TEST"})));
});
