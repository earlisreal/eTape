// @vitest-environment jsdom
import { render,screen,act,waitFor,cleanup,fireEvent } from "@testing-library/react";
import { expect,it,vi,afterEach } from "vitest";
import { PriceAlertNotifications } from "./PriceAlertNotifications";
import { PriceAlertStore } from "../../data/PriceAlertStore";
import { soundEngine } from "../../sound/SoundEngine";
import { LinkGroups } from "../linkGroups";
import type { AlertDestination } from "./navigation";

const groups=()=>new LinkGroups({post:()=>{},onMessage:()=>()=>{},close:()=>{}},()=>{});
afterEach(()=>{cleanup();vi.restoreAllMocks();});
it("only announces live deltas and claims a burst once for sound",async()=>{
 const store=new PriceAlertStore();
 const play=vi.spyOn(soundEngine,"priceAlert").mockImplementation(()=>{});
 vi.spyOn(soundEngine,"canPlayPriceAlert").mockReturnValue(true);
 const sendCommand=vi.fn(async(name:string,args:unknown)=>{void name;void args;return {kind:"ack" as const,corrId:"c",status:"accepted" as const,value:true};});
 const navigation={destination:vi.fn(async()=>({group:"red" as const,unavailable:false})),subscribe:()=>()=>{}};
 render(<PriceAlertNotifications store={store} commands={{sendCommand}} onViewAlerts={()=>{}} navigation={navigation} linkGroups={groups()}/>);
 const event={id:"burst",expiresMs:Date.now()+5000,triggers:[{id:"t",symbol:"US.TEST",price:5.5,actualPrice:5.6}]};
 act(()=>store.apply({kind:"snapshot",topic:"alerts.trigger",payload:event}));expect(play).not.toHaveBeenCalled();
 act(()=>store.apply({kind:"delta",topic:"alerts.trigger",payload:event}));
 await waitFor(()=>expect(screen.getByRole("status").textContent).toContain("Price alert"));
 act(()=>store.apply({kind:"delta",topic:"alerts.trigger",payload:event}));
 await waitFor(()=>expect(play).toHaveBeenCalledTimes(1));
 expect(sendCommand.mock.calls.filter(call=>call[0]==="ClaimPriceAlertNotification"&&(call[1] as {kind:string}).kind==="sound")).toHaveLength(1);
});

it("rechecks the popup destination on activation and routes the displayed trigger only",async()=>{
 const store=new PriceAlertStore(),linkGroups=groups(),focus=vi.spyOn(linkGroups,"focus");
 let target:AlertDestination={group:"red",unavailable:false};
 const navigation={destination:vi.fn(async()=>target),subscribe:()=>()=>{}};
 const sendCommand=vi.fn(async()=>({kind:"ack" as const,corrId:"c",status:"accepted" as const,value:true}));
 render(<PriceAlertNotifications store={store} commands={{sendCommand}} onViewAlerts={()=>{}} navigation={navigation} linkGroups={linkGroups}/>);
 const event={id:"burst",expiresMs:Date.now()+5000,triggers:[{id:"t",symbol:"US.TEST",sourceGroup:"blue",price:5.5,actualPrice:5.6},{id:"t2",symbol:"US.OTHER",sourceGroup:"green"}]};
 act(()=>store.apply({kind:"delta",topic:"alerts.trigger",payload:event}));
 const button=await screen.findByRole("button",{name:"Load TEST into red group"});
 expect(focus).not.toHaveBeenCalled();
 target={group:"yellow",unavailable:false};
 fireEvent.click(button);
 await waitFor(()=>expect(focus).toHaveBeenCalledWith("yellow","US.TEST"));
 expect(navigation.destination).toHaveBeenCalledWith("blue");
 expect(navigation.destination.mock.calls.length).toBeGreaterThanOrEqual(2);
 expect(focus).toHaveBeenCalledTimes(1);
 expect(screen.queryByRole("button",{name:"View chart"})).toBeNull();
});

it.each([false,true])("keeps View alerts available when symbol navigation is blocked (unavailable=%s)",async(unavailable)=>{
 const store=new PriceAlertStore(),linkGroups=groups(),focus=vi.spyOn(linkGroups,"focus"),view=vi.fn();
 const navigation={destination:async()=>({group:null,unavailable}),subscribe:()=>()=>{}};
 const sendCommand=async()=>({kind:"ack" as const,corrId:"c",status:"accepted" as const,value:true});
 render(<PriceAlertNotifications store={store} commands={{sendCommand}} onViewAlerts={view} navigation={navigation} linkGroups={linkGroups}/>);
 act(()=>store.apply({kind:"delta",topic:"alerts.trigger",payload:{id:"burst",expiresMs:Date.now()+5000,triggers:[{symbol:"US.TEST"}]}}));
 const name=unavailable?"Alert navigation temporarily unavailable":"Choose a Link Group color in an Alerts Panel to load symbols";
 const symbol=await screen.findByRole<HTMLButtonElement>("button",{name});
 expect(symbol.disabled).toBe(true);fireEvent.click(symbol);
 fireEvent.click(screen.getByRole("button",{name:"View alerts"}));
 expect(view).toHaveBeenCalledTimes(1);expect(focus).not.toHaveBeenCalled();
});
