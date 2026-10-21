import { Button } from "../controls/Button";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { AckMsg, PriceAlert, PriceAlertPreview } from "../../wire/contract";
import { modalTracker } from "../modalTracker";

export interface AlertCommands { sendCommand(name:string,args:unknown):Promise<AckMsg> }
export interface AlertEdit { alert?:PriceAlert; symbol:string; price?:number; rearm?:boolean; sourceGroup?:string|null }

export function PriceAlertEditor({commands,symbol,price,alert,rearm=false,sourceGroup,onClose}:AlertEdit & {commands:AlertCommands;onClose:()=>void}):JSX.Element {
 const [target,setTarget]=useState(String(price??alert?.price??""));
 const [condition,setCondition]=useState(alert?.condition??"at-or-above");
 const touched=useRef(!!alert);
 const [label,setLabel]=useState(alert?.label??"");
 const [sessions,setSessions]=useState(alert?.sessions??{pre:true,rth:true,post:true});
 const [last,setLast]=useState<number|null>(null);
 const [error,setError]=useState("");
 const [busy,setBusy]=useState(false);
 const [newSymbol,setSymbol]=useState(symbol);
 const field=useRef<HTMLInputElement>(null);
 const effectiveSymbol=(newSymbol.trim().toUpperCase().startsWith("US.")?newSymbol:`US.${newSymbol.trim()}`).toUpperCase().trim();
 useEffect(()=>{const previous=document.activeElement;field.current?.focus();modalTracker.setOpen(true);return()=>{modalTracker.setOpen(false);if(previous instanceof HTMLElement)previous.focus();};},[]);
 useEffect(()=>{
  let alive=true;setLast(null);
  if(newSymbol.trim()) void commands.sendCommand("PreviewPriceAlert",{symbol:effectiveSymbol}).then(ack=>{
   if(!alive||ack.status!=="accepted")return;
   const p=ack.value as PriceAlertPreview|undefined;
   if(p&&p.price>0){setLast(p.price);if(!touched.current)setCondition(Number(target)>=p.price?"at-or-above":"at-or-below");}
  }).catch(()=>{});
  return()=>{alive=false;};
 // Target changes must not request a new live preview or overwrite explicit direction.
 },[commands,effectiveSymbol]);
 const value=Number(target);
 const met=last!==null&&(condition==="at-or-above"?last>=value:last<=value);
 const save=async()=>{
  if(!Number.isFinite(value)||value<=0||!newSymbol.trim()){setError("Enter a symbol and positive target price.");return;}
  if(!sessions.pre&&!sessions.rth&&!sessions.post){setError("Select at least one session.");return;}
  setBusy(true);setError("");
  try{const ack=await commands.sendCommand("UpsertPriceAlert",{id:alert?.id??"",version:alert?.version??0,rearm,symbol:effectiveSymbol,sourceGroup:alert?alert.sourceGroup:sourceGroup??undefined,condition,price:value,label,sessions});
   if(ack.status!=="accepted")setError(ack.reason??"Could not save alert.");else onClose();
  }catch(e){setError(String(e));}finally{setBusy(false);}
 };
 return createPortal(<div data-drawing-ui="true" onPointerDown={e=>e.stopPropagation()} onKeyDown={e=>{e.stopPropagation();if(e.key==="Escape"&&!busy)onClose();if(e.key==="Tab"){const focusable=[...e.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),select:not(:disabled)')];const first=focusable[0],last=focusable.at(-1);if(e.shiftKey&&document.activeElement===first){e.preventDefault();last?.focus();}else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first?.focus();}}}}
  style={{position:"fixed",inset:0,zIndex:1000,background:"#0007",display:"grid",placeItems:"center"}}>
  <form role="dialog" aria-modal="true" aria-label={rearm?"Rearm price alert":"Price alert"} onSubmit={e=>{e.preventDefault();void save();}}
   className="popover" style={{position:"relative",width:"min(370px, calc(100vw - 24px))",padding:18,display:"grid",gap:12,maxHeight:"90vh",overflow:"auto"}}>
   <strong>{rearm?"Rearm price alert":alert?"Edit price alert":"Create price alert"}</strong>
   <label>Symbol <input className="field" aria-label="Symbol" value={newSymbol} disabled={!!alert||!!symbol} onChange={e=>setSymbol(e.target.value)} maxLength={20}/></label>
   <label>Condition <select className="field" aria-label="Condition" value={condition} onChange={e=>{touched.current=true;setCondition(e.target.value);}}>
    <option value="at-or-above">At or above</option><option value="at-or-below">At or below</option></select></label>
   <label>Target price <input className="field" ref={field} aria-label="Target price" type="number" min="0.00000001" step="any" required value={target} onChange={e=>{setTarget(e.target.value);if(!touched.current&&last!==null)setCondition(Number(e.target.value)>=last?"at-or-above":"at-or-below");}}/></label>
   <label>Label <input className="field" aria-label="Label" maxLength={200} value={label} onChange={e=>setLabel(e.target.value)}/></label>
   <fieldset><legend>Sessions</legend>{([["pre","Pre-market"],["rth","Regular"],["post","After-hours"]] as const).map(([key,name])=><label key={key} style={{display:"block"}}><input type="checkbox" checked={sessions[key]} onChange={e=>setSessions({...sessions,[key]:e.target.checked})}/>{name}</label>)}</fieldset>
   {met&&<div role="alert" style={{color:"#c58c35"}}>Condition already met. The next fresh eligible trade can trigger this alert.</div>}
   {last===null&&<small>Waiting for a trusted live price. The next fresh eligible trade will evaluate your target.</small>}
   {alert?.state==="triggered"&&!rearm&&<small>This alert stays Triggered. Use Rearm to monitor again.</small>}
   <small>Last-Eligible Price · fires once · saved across workspaces and restarts. Absolute dollar level; review after stock splits.</small>
   {error&&<div role="alert">{error}</div>}
   <div style={{display:"flex",justifyContent:"flex-end",gap:8}}><Button type="button" disabled={busy} onClick={onClose}>Cancel</Button><Button disabled={busy} type="submit">{busy?"Saving…":"Save alert"}</Button></div>
  </form>
 </div>,document.body);
}
