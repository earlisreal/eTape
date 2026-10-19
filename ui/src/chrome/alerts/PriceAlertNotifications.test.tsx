// @vitest-environment jsdom
import { render,screen,act,waitFor,cleanup } from "@testing-library/react";
import { expect,it,vi,afterEach } from "vitest";
import { PriceAlertNotifications } from "./PriceAlertNotifications";
import { PriceAlertStore } from "../../data/PriceAlertStore";
import { soundEngine } from "../../sound/SoundEngine";
afterEach(()=>{cleanup();vi.restoreAllMocks();});
it("only announces live deltas and claims a burst once for sound",async()=>{
 const store=new PriceAlertStore();
 const play=vi.spyOn(soundEngine,"priceAlert").mockImplementation(()=>{});
 vi.spyOn(soundEngine,"canPlayPriceAlert").mockReturnValue(true);
 const sendCommand=vi.fn(async(name:string,args:unknown)=>{void name;void args;return {kind:"ack" as const,corrId:"c",status:"accepted" as const,value:true};});
 render(<PriceAlertNotifications store={store} commands={{sendCommand}} onViewAlerts={()=>{}}/>);
 const event={id:"burst",expiresMs:Date.now()+5000,triggers:[{id:"t",symbol:"US.TEST",price:5.5,actualPrice:5.6}]};
 act(()=>store.apply({kind:"snapshot",topic:"alerts.trigger",payload:event}));expect(play).not.toHaveBeenCalled();
 act(()=>store.apply({kind:"delta",topic:"alerts.trigger",payload:event}));
 await waitFor(()=>expect(screen.getByRole("status").textContent).toContain("Price alert"));
 act(()=>store.apply({kind:"delta",topic:"alerts.trigger",payload:event}));
 await waitFor(()=>expect(play).toHaveBeenCalledTimes(1));
 expect(sendCommand.mock.calls.filter(call=>call[0]==="ClaimPriceAlertNotification"&&(call[1] as {kind:string}).kind==="sound")).toHaveLength(1);
});
