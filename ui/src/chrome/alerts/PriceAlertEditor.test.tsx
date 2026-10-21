// @vitest-environment jsdom
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import { expect, it, vi, afterEach } from "vitest";
import { PriceAlertEditor } from "./PriceAlertEditor";
import type { PriceAlert } from "../../wire/contract";
afterEach(cleanup);

it("warns for a met condition and saves the exact target only on Save", async () => {
 const sendCommand=vi.fn(async(name:string)=>({kind:"ack" as const,corrId:"1",status:"accepted" as const,value:name==="PreviewPriceAlert"?{price:5.7,tsMs:1}:undefined}));
 const close=vi.fn();
 render(<PriceAlertEditor commands={{sendCommand}} symbol="US.TEST" price={5.5} sourceGroup="blue" onClose={close}/>);
 await waitFor(()=>expect(screen.getByLabelText<HTMLSelectElement>("Condition").value).toBe("at-or-below"));
 fireEvent.change(screen.getByLabelText("Condition"),{target:{value:"at-or-above"}});
 expect(screen.getByRole("alert").textContent).toContain("already met");
 expect(sendCommand).toHaveBeenCalledTimes(1);
 fireEvent.change(screen.getByLabelText("Target price"),{target:{value:"5.4321"}});
 fireEvent.click(screen.getByRole("button",{name:"Save alert"}));
 await waitFor(()=>expect(close).toHaveBeenCalled());
 expect(sendCommand).toHaveBeenLastCalledWith("UpsertPriceAlert",expect.objectContaining({price:5.4321,sourceGroup:"blue",sessions:{pre:true,rth:true,post:true}}));
});

it.each([undefined,"blue"])("preserves source %s through Rearm from another color",async(sourceGroup)=>{
 const alert:PriceAlert={id:"a",symbol:"US.TEST",...(sourceGroup?{sourceGroup}:{}),price:5,condition:"at-or-above",sessions:{pre:true,rth:true,post:true},label:"",state:"triggered",version:1,createdMs:1,updatedMs:1};
 const sendCommand=vi.fn(async()=>({kind:"ack" as const,corrId:"c",status:"accepted" as const}));
 render(<PriceAlertEditor commands={{sendCommand}} symbol="US.TEST" alert={alert} rearm sourceGroup="red" onClose={()=>{}}/>);
 fireEvent.click(screen.getByRole("button",{name:"Save alert"}));
 await waitFor(()=>expect(sendCommand).toHaveBeenCalledWith("UpsertPriceAlert",expect.objectContaining({sourceGroup,rearm:true})));
});
// @vitest-environment jsdom
