import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { PriceAlertEditor } from "./PriceAlertEditor";

it("warns for a met condition and saves the exact target only on Save", async () => {
 const sendCommand=vi.fn(async(name:string)=>({kind:"ack" as const,corrId:"1",status:"accepted" as const,value:name==="PreviewPriceAlert"?{price:5.7,tsMs:1}:undefined}));
 const close=vi.fn();
 render(<PriceAlertEditor commands={{sendCommand}} symbol="US.TEST" price={5.5} onClose={close}/>);
 await waitFor(()=>expect(screen.getByLabelText<HTMLSelectElement>("Condition").value).toBe("at-or-below"));
 fireEvent.change(screen.getByLabelText("Condition"),{target:{value:"at-or-above"}});
 expect(screen.getByRole("alert").textContent).toContain("already met");
 expect(sendCommand).toHaveBeenCalledTimes(1);
 fireEvent.change(screen.getByLabelText("Target price"),{target:{value:"5.4321"}});
 fireEvent.click(screen.getByRole("button",{name:"Save alert"}));
 await waitFor(()=>expect(close).toHaveBeenCalled());
 expect(sendCommand).toHaveBeenLastCalledWith("UpsertPriceAlert",expect.objectContaining({price:5.4321,sessions:{pre:true,rth:true,post:true}}));
});
// @vitest-environment jsdom
