import { Button } from "../controls/Button";
import { useEffect,useRef,useState } from "react";
import type { PriceAlertNotification } from "../../wire/contract";
import type { PriceAlertStore } from "../../data/PriceAlertStore";
import type { AlertCommands } from "./PriceAlertEditor";
import { soundEngine } from "../../sound/SoundEngine";
import { viewAlertChart } from "./navigation";

export function PriceAlertNotifications({store,commands,onViewAlerts}:{store:PriceAlertStore;commands:AlertCommands;onViewAlerts:()=>void}):JSX.Element|null {
 const [popup,setPopup]=useState<PriceAlertNotification|null>(null);
 const seen=useRef(new Map<string,number>());
 useEffect(()=>{
  let alive=true;
  const off=store.onNotification(n=>{
   for(const [id,expiry]of seen.current)if(expiry<Date.now())seen.current.delete(id);
   const claim=async(kind:"sound"|"popup")=>{
    try{const ack=await commands.sendCommand("ClaimPriceAlertNotification",{id:n.id,kind});
     if(!alive||ack.status!=="accepted"||ack.value!==true)return;
     if(kind==="sound")soundEngine.priceAlert();
     else setPopup(old=>old?.id===n.id&&old.triggers.length>n.triggers.length?old:n);
    }catch{/* A dropped connection never queues a notification for later. */}
   };
   if(!seen.current.has(n.id)){
    seen.current.set(n.id,n.expiresMs);
    if(soundEngine.canPlayPriceAlert())void claim("sound");
   }
   void claim("popup");
  });
  return()=>{alive=false;off();};
 },[store,commands]);
 useEffect(()=>{if(!popup)return;const id=setTimeout(()=>setPopup(null),7000);return()=>clearTimeout(id);},[popup]);
 if(!popup)return null;
 const first=popup.triggers[0];
 return <div role="status" className="popover" style={{position:"fixed",right:16,bottom:20,zIndex:90,padding:14,width:"min(320px, calc(100vw - 32px))",boxSizing:"border-box"}}>
  <strong>{popup.triggers.length===1?"Price alert":`${popup.triggers.length} price alerts`}</strong>
  {first&&<p>{first.symbol.replace(/^US\./,"")} {first.condition==="at-or-below"?"≤":"≥"} ${first.price} · traded ${first.actualPrice}<br/><small>{new Date(first.tsMs).toLocaleTimeString()} · {first.reason}</small></p>}
  <div style={{display:"flex",gap:8}}><Button onClick={onViewAlerts}>View alerts</Button>{first&&<Button onClick={()=>viewAlertChart(first.symbol)}>View chart</Button>}<Button aria-label="Dismiss price alert" onClick={()=>setPopup(null)}>×</Button></div>
 </div>;
}
