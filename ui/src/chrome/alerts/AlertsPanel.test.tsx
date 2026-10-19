// @vitest-environment jsdom
import { render,screen,fireEvent,cleanup } from "@testing-library/react";
import { expect,it,vi,afterEach } from "vitest";
import { AlertsPanel } from "../panels/AlertsPanel";
import { makeStores } from "../../data/registry";
import type { PriceAlertTrigger } from "../../wire/contract";
afterEach(cleanup);
it("keeps deleted alert history readable and navigates only on View chart",()=>{
 const stores=makeStores();
 const history:PriceAlertTrigger={id:"h",alertId:"deleted",symbol:"US.TEST",condition:"at-or-above",price:5.5,actualPrice:5.6,label:"Breakout",sessions:{pre:true,rth:true,post:true},session:"rth",tsMs:1,reason:"Target reached"};
 stores.priceAlerts.apply({kind:"snapshot",topic:"alerts.state",payload:{alerts:[],history:[history],historyTotal:1,revision:1,error:""}});
 const navigate=vi.fn();window.addEventListener("etape:alert-chart",navigate);
 const sendCommand=vi.fn(async()=>({kind:"ack" as const,corrId:"c",status:"accepted" as const}));
 render(<AlertsPanel stores={stores} commands={{sendCommand,sendQuery:async()=>null}}/>);
 fireEvent.click(screen.getByRole("tab",{name:"History (1)"}));
 expect(screen.getByText("Alert deleted")).toBeTruthy();expect(navigate).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button",{name:"View chart"}));expect(navigate).toHaveBeenCalledTimes(1);
 expect(sendCommand).not.toHaveBeenCalled();window.removeEventListener("etape:alert-chart",navigate);
});
