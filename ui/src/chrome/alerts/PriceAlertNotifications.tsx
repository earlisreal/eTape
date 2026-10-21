import { Button } from "../controls/Button";
import { useEffect,useRef,useState } from "react";
import type { PriceAlertNotification } from "../../wire/contract";
import type { PriceAlertStore } from "../../data/PriceAlertStore";
import type { AlertCommands } from "./PriceAlertEditor";
import { soundEngine } from "../../sound/SoundEngine";
import type { AlertDestination, AlertNavigation } from "./navigation";
import type { LinkGroups } from "../linkGroups";

export function PriceAlertNotifications({store,commands,onViewAlerts,navigation,linkGroups}:{store:PriceAlertStore;commands:AlertCommands;onViewAlerts:()=>void;navigation:Pick<AlertNavigation,"destination"|"subscribe">;linkGroups:LinkGroups}):JSX.Element|null {
 const [popup,setPopup]=useState<PriceAlertNotification|null>(null);
 const [destination,setDestination]=useState<AlertDestination>({group:null,unavailable:true});
 const [navigating,setNavigating]=useState(false);
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
 useEffect(()=>{
  let alive=true,revision=0;
  setDestination({group:null,unavailable:true});
  if(!popup)return;
  const update=()=>{const current=++revision;void navigation.destination(popup.triggers[0]?.sourceGroup).then(next=>{if(alive&&revision===current)setDestination(next);});};
  const off=navigation.subscribe(update);window.addEventListener("focus",update);update();
  return()=>{alive=false;off();window.removeEventListener("focus",update);};
 },[popup,navigation]);
 if(!popup)return null;
 const first=popup.triggers[0];
 const hint=destination.unavailable?"Alert navigation temporarily unavailable":"Choose a Link Group color in an Alerts Panel to load symbols";
 const title=destination.group?`Load ${first?.symbol.replace(/^US\./,"")} into ${destination.group} group`:hint;
 const navigate=async()=>{
  if(!first)return;
  setNavigating(true);
  try{const next=await navigation.destination(first.sourceGroup);setDestination(next);if(next.group)linkGroups.focus(next.group,first.symbol);}
  finally{setNavigating(false);}
 };
 return <div role="status" className="popover" style={{position:"fixed",right:16,bottom:20,zIndex:90,padding:14,width:"min(320px, calc(100vw - 32px))",boxSizing:"border-box"}}>
  <strong>{popup.triggers.length===1?"Price alert":`${popup.triggers.length} price alerts`}</strong>
  {first&&<p><Button variant="quiet" style={{padding:0,fontWeight:700}} title={title} aria-label={title} disabled={!destination.group} loading={navigating} onClick={()=>void navigate()}>{first.symbol.replace(/^US\./,"")}</Button> {first.condition==="at-or-below"?"≤":"≥"} ${first.price} · traded ${first.actualPrice}<br/><small>{new Date(first.tsMs).toLocaleTimeString()} · {first.reason}</small></p>}
  {!destination.group&&<p style={{fontSize:11}}>{hint}.</p>}
  <div style={{display:"flex",gap:8}}><Button onClick={onViewAlerts}>View alerts</Button><Button aria-label="Dismiss price alert" onClick={()=>setPopup(null)}>×</Button></div>
 </div>;
}
