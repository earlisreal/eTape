import { Button } from "../controls/Button";
import { useEffect,useState,useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { PriceAlert,PriceAlertTrigger } from "../../wire/contract";
import { PriceAlertEditor,type AlertEdit } from "../alerts/PriceAlertEditor";
import { viewAlertChart } from "../alerts/navigation";
import { alertPriceText as price } from "../alerts/price";

const states:Record<string,string>={monitoring:"Monitoring","waiting-data":"Waiting for data","waiting-capacity":"Waiting for data capacity","outside-session":"Outside selected sessions",paused:"Paused",triggered:"Triggered"};
export function AlertsPanel({stores,commands}:Pick<PanelProps,"stores"|"commands">):JSX.Element {
 const s=useSyncExternalStore(cb=>stores.priceAlerts.subscribe(cb),()=>stores.priceAlerts.getSnapshot());
 const [tab,setTab]=useState<"alerts"|"history">("alerts");
 const [edit,setEdit]=useState<AlertEdit|null>(null);
 const [older,setOlder]=useState<PriceAlertTrigger[]>([]);
 const [error,setError]=useState("");
 const [busy,setBusy]=useState(false);
 const [selected,setSelected]=useState("");
 useEffect(()=>setOlder([]),[s.revision]);
 const action=async(name:string,args:unknown)=>{setError("");try{const ack=await commands.sendCommand(name,args);if(ack.status!=="accepted")setError(ack.reason??"Action failed");}catch(e){setError(String(e));}};
 const row=(a:PriceAlert)=><article key={a.id} id={`price-alert-${a.id}`} data-selected={selected===a.id} onClick={()=>setSelected(a.id)} style={{borderBottom:"1px solid var(--border)",borderLeft:selected===a.id?"3px solid var(--accent)":"3px solid transparent",padding:"10px 6px",display:"grid",gap:6}}>
  <div><strong>{a.symbol.replace(/^US\./,"")} {a.condition==="at-or-above"?"≥":"≤"} ${price(a.price)}</strong> <span style={{color:a.state==="triggered"?"#c58c35":undefined}}>{states[a.state]??a.state}</span></div>
  {a.label&&<div>{a.label}</div>}<small>{[a.sessions.pre&&"Pre-market",a.sessions.rth&&"Regular",a.sessions.post&&"After-hours"].filter(Boolean).join(" · ")}</small>
  <div style={{display:"flex",gap:5,flexWrap:"wrap"}}>
   <Button onClick={()=>setEdit({symbol:a.symbol,alert:a})}>Edit</Button>
   {a.state==="triggered"?<Button onClick={()=>setEdit({symbol:a.symbol,alert:a,rearm:true})}>Rearm</Button>:<Button onClick={()=>void action("PausePriceAlert",{id:a.id,version:a.version,paused:a.state!=="paused"})}>{a.state==="paused"?"Resume":"Pause"}</Button>}
   <Button onClick={()=>viewAlertChart(a.symbol)}>View chart</Button>
   <Button onClick={()=>void action("DeletePriceAlert",{id:a.id,version:a.version})}>Delete</Button>
  </div>
 </article>;
 const history=[...s.history,...older.filter(t=>!s.history.some(h=>h.id===t.id))];
 const more=async()=>{setBusy(true);setError("");try{const ack=await commands.sendCommand("QueryPriceAlertHistory",{beforeId:history.at(-1)?.id??""});if(ack.status==="accepted")setOlder(prev=>[...prev,...ack.value as PriceAlertTrigger[]]);else setError(ack.reason??"Unable to load history");}catch(e){setError(String(e));}finally{setBusy(false);}};
 return <div style={{height:"100%",overflow:"auto",padding:12,fontSize:12,boxSizing:"border-box"}}>
  <div role="tablist" style={{display:"flex",gap:8,alignItems:"center"}}><Button role="tab" aria-selected={tab==="alerts"} onClick={()=>setTab("alerts")}>Alerts ({s.alerts.length})</Button><Button role="tab" aria-selected={tab==="history"} onClick={()=>setTab("history")}>History ({s.historyTotal})</Button><Button style={{marginLeft:"auto"}} onClick={()=>setEdit({symbol:""})}>+ Alert</Button></div>
  {(error||s.error)&&<div role="alert">{error||s.error}</div>}
  {tab==="alerts"?<>{s.alerts.length?s.alerts.map(row):<p>Create an alert from a chart’s bell tool or add one here.</p>}<p style={{opacity:.65}}>Saved across workspaces and restarts. Review absolute price levels after stock splits.</p></>:<>{history.map(t=><article key={t.id} style={{padding:"10px 0",borderBottom:"1px solid var(--border)",display:"grid",gap:5}}>
   <strong>{t.symbol.replace(/^US\./,"")} {t.condition==="at-or-above"?"≥":"≤"} ${price(t.price)} · traded ${price(t.actualPrice)}</strong>
   <span>{t.label} · {t.reason}</span><small>{new Date(t.tsMs).toLocaleString()} · {t.session} · {[t.sessions.pre&&"Pre-market",t.sessions.rth&&"Regular",t.sessions.post&&"After-hours"].filter(Boolean).join(" / ")}</small>
   <div><Button onClick={()=>viewAlertChart(t.symbol)}>View chart</Button> {s.alerts.some(a=>a.id===t.alertId)?<Button onClick={()=>{setTab("alerts");setSelected(t.alertId);requestAnimationFrame(()=>document.getElementById(`price-alert-${t.alertId}`)?.scrollIntoView?.({block:"nearest"}));}}>View alert</Button>:<span>Alert deleted</span>} <Button onClick={()=>void action("DeletePriceAlertTrigger",{id:t.id})}>Delete history entry</Button></div>
  </article>)}{history.length===0&&<p>No triggered alerts yet.</p>}{history.length<s.historyTotal&&<Button disabled={busy} onClick={()=>void more()}>{busy?"Loading…":"Load older history"}</Button>}</>}
  {edit&&<PriceAlertEditor {...edit} commands={commands} onClose={()=>setEdit(null)}/>}
 </div>;
}
