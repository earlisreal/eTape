import { useEffect,useRef,useState,useSyncExternalStore,type MutableRefObject,type PointerEvent as ReactPointerEvent } from "react";
import type { ChartApiFacade } from "../../render/chart/ChartApiFacade";
import type { PriceAlert } from "../../wire/contract";
import type { PriceAlertStore } from "../../data/PriceAlertStore";
import { PriceAlertEditor,type AlertCommands,type AlertEdit } from "./PriceAlertEditor";
import { attachOrderPillLayer } from "../panels/tv/orderPillLayer";
import { IconBell } from "../panels/tv/tvIcons";
import { snapAlertPrice,alertPriceText } from "./price";
import type { LinkGroup } from "../linkGroups";

interface Props {
 store:PriceAlertStore;commands:AlertCommands;symbol:string;
 group?:LinkGroup;
 hostRef:MutableRefObject<HTMLDivElement|null>;
 facadeRef:MutableRefObject<Pick<ChartApiFacade,"priceToCoordinate"|"coordinateToPrice"|"setPanZoomEnabled">|null>;
 layoutRef:MutableRefObject<()=>void>;
 rightAxisWidth:number;paneHeight:number;enabled:boolean;
 armed?:boolean;onDisarm?:()=>void;onEdit?:()=>void;
}
export function ChartPriceAlerts(props:Props):JSX.Element {
 const snapshot=useSyncExternalStore(cb=>props.store.subscribe(cb),()=>props.store.getSnapshot());
 const alerts=snapshot.alerts.filter(a=>a.symbol===props.symbol&&a.state!=="triggered");
 const root=useRef<HTMLDivElement>(null);
 const latest=useRef({...props,alerts});latest.current={...props,alerts};
 const layer=useRef<ReturnType<typeof attachOrderPillLayer>|null>(null);
 const drag=useRef<{alert:PriceAlert;price:number;moved:boolean;start:number;cleanup:()=>void}|null>(null);
 const hover=useRef<{x:number;y:number}|null>(null);
 const suppressClick=useRef(false);
 const [edit,setEdit]=useState<AlertEdit|null>(null);
 const [choices,setChoices]=useState<PriceAlert[]|null>(null);
 const open=(alert:PriceAlert,price=alert.price)=>{props.onEdit?.();setChoices(null);setEdit({symbol:alert.symbol,alert,price});};
 const layout=()=>{
  const p=latest.current,facade=p.facadeRef.current;
  if(!facade||!root.current)return;
  const rows=[...root.current.querySelectorAll<HTMLElement>("[data-alert-row]")];
  const visible:{row:HTMLElement;y:number;alert:PriceAlert}[]=[];
  for(const row of rows){
   const a=p.alerts.find(a=>a.id===row.dataset.alertRow);if(!a)continue;
   const price=drag.current?.alert.id===a.id?drag.current.price:a.price;
   const y=facade.priceToCoordinate(price);
   row.style.display=y===null||!Number.isFinite(y)||y<12||y>p.paneHeight-12?"none":"block";
   if(y===null||row.style.display==="none")continue;
   row.style.top=`${Math.round(y)}px`;visible.push({row,y,alert:a});
   const chip=row.querySelector<HTMLElement>("[data-alert-chip]");
   if(chip){chip.style.display="flex";chip.dataset.ids=a.id;
    const text=chip.querySelector("[data-alert-text]");if(text)text.textContent=alertPriceText(price);}
  }
  visible.sort((a,b)=>a.y-b.y);
  for(let i=0;i<visible.length;){
   const group=[visible[i++]];
   while(i<visible.length&&visible[i].y-group[0].y<21)group.push(visible[i++]);
   const chip=group[0].row.querySelector<HTMLElement>("[data-alert-chip]");
   if(chip&&group.length>1){chip.dataset.ids=group.map(v=>v.alert.id).join(",");const text=chip.querySelector("[data-alert-text]");if(text)text.textContent=`${group.length} alerts`;}
   for(const v of group.slice(1)){const tag=v.row.querySelector<HTMLElement>("[data-alert-chip]");if(tag)tag.style.display="none";}
  }
  const preview=root.current.querySelector<HTMLElement>("[data-alert-preview]");
  if(preview){
   const point=hover.current;
   const price=point&&p.armed?facade.coordinateToPrice(point.y):null;
   preview.style.display=price!==null&&price>0&&point&&point.y>12&&point.y<p.paneHeight-12?"block":"none";
   if(point){preview.style.top=`${point.y}px`;const text=preview.querySelector("span");if(text&&price!==null)text.textContent=alertPriceText(price);}
  }
  layer.current?.sync();
 };
 useEffect(()=>{
  const host=props.hostRef.current;if(!host||!root.current)return;
  const owner=attachOrderPillLayer(host,root.current);layer.current=owner;
  return()=>{owner.dispose();layer.current=null;drag.current?.cleanup();};
 },[props.hostRef]);
 useEffect(()=>{props.layoutRef.current=layout;layout();return()=>{props.layoutRef.current=()=>{};};});
 useEffect(()=>{
  const host=props.hostRef.current;if(!host)return;
  const down=(e:PointerEvent)=>{
   if(!props.armed||e.button!==0||e.target instanceof Element&&e.target.closest("[data-drawing-ui]"))return;
   const r=host.getBoundingClientRect(),y=e.clientY-r.top;
   if(y<0||y>props.paneHeight||e.clientX-r.left>r.width-props.rightAxisWidth)return;
   const price=props.facadeRef.current?.coordinateToPrice(y);
   e.preventDefault();e.stopImmediatePropagation();props.onDisarm?.();
   if(price!==null&&price!==undefined&&price>0)setEdit({symbol:props.symbol,price:snapAlertPrice(price),sourceGroup:props.group??null});
  };
  let frame=0;
  const move=(e:PointerEvent)=>{const r=host.getBoundingClientRect();hover.current={x:e.clientX-r.left,y:e.clientY-r.top};if(!frame)frame=requestAnimationFrame(()=>{frame=0;layout();});};
  const leave=()=>{hover.current=null;layout();};
  const cancel=()=>{drag.current?.cleanup();drag.current=null;layout();props.onDisarm?.();};
  const key=(e:KeyboardEvent)=>{if(e.key==="Escape"){cancel();setChoices(null);}};
  host.addEventListener("pointerdown",down,true);
  host.addEventListener("pointermove",move);host.addEventListener("pointerleave",leave);
  window.addEventListener("keydown",key);window.addEventListener("blur",cancel);window.addEventListener("pointercancel",cancel);host.addEventListener("contextmenu",cancel);
  return()=>{cancelAnimationFrame(frame);host.removeEventListener("pointermove",move);host.removeEventListener("pointerleave",leave);host.removeEventListener("pointerdown",down,true);window.removeEventListener("keydown",key);window.removeEventListener("blur",cancel);window.removeEventListener("pointercancel",cancel);host.removeEventListener("contextmenu",cancel);};
 },[props.armed,props.symbol,props.group,props.paneHeight,props.rightAxisWidth,props.hostRef,props.facadeRef,props.onDisarm]);
 const down=(e:ReactPointerEvent,alert:PriceAlert)=>{
  const ids=e.currentTarget.parentElement?.dataset.ids?.split(",")??[];
  if(!props.enabled||ids.length>1||e.button!==0||e.altKey||e.shiftKey||e.ctrlKey||e.metaKey)return;
  e.preventDefault();e.stopPropagation();
  const host=props.hostRef.current;if(!host)return;
  const chip=e.currentTarget.parentElement;
  props.facadeRef.current?.setPanZoomEnabled(false);layer.current?.drag(chip);
  const cleanup=()=>{window.removeEventListener("pointermove",move,true);window.removeEventListener("pointerup",up,true);props.facadeRef.current?.setPanZoomEnabled(true);layer.current?.drag(null);drag.current=null;};
  const move=(ev:PointerEvent)=>{const d=drag.current;if(!d)return;const y=ev.clientY-host.getBoundingClientRect().top;const price=props.facadeRef.current?.coordinateToPrice(y);if(price===null||price===undefined||price<=0||y<0||y>props.paneHeight)return;d.moved ||=Math.abs(ev.clientY-d.start)>3;d.price=snapAlertPrice(price);layout();};
  const up=(ev:PointerEvent)=>{const d=drag.current;cleanup();if(!d)return;ev.preventDefault();ev.stopImmediatePropagation();suppressClick.current=d.moved;if(d.moved)open(alert,d.price);else open(alert);layout();};
  drag.current={alert,price:alert.price,moved:false,start:e.clientY,cleanup};
  window.addEventListener("pointermove",move,true);window.addEventListener("pointerup",up,true);
 };
 return <>
  <div ref={root} data-drawing-ui="true" style={{position:"absolute",inset:0,zIndex:7,pointerEvents:"none",overflow:"hidden"}}>
   <div data-alert-preview style={{display:"none",position:"absolute",left:0,right:0,height:0}}>
    <div data-order-line style={{position:"absolute",left:0,right:props.rightAxisWidth,borderTop:"1px dashed #c89642"}}/>
    <div data-alert-chip style={{position:"absolute",right:0,top:-10,height:20,opacity:0,color:"#e9b55e",background:"#191b20",border:"1px solid #c89642",padding:"0 5px",display:"flex",alignItems:"center",gap:4}}><IconBell size={12}/><span/></div>
   </div>
   {alerts.map(a=><div key={a.id} data-alert-row={a.id} style={{position:"absolute",left:0,right:0,height:0,opacity:a.state==="paused"?.4:.85}}>
    <div data-order-line style={{position:"absolute",left:0,right:props.rightAxisWidth,borderTop:"1px dashed #c89642",pointerEvents:"none"}}/>
    <div data-alert-chip style={{position:"absolute",right:0,top:-10,height:20,opacity:0,display:"flex",alignItems:"center",border:"1px solid #c89642",borderRadius:3,background:"#191b20",color:"#e9b55e",pointerEvents:props.enabled?"auto":"none"}}>
     <button disabled={!props.enabled} aria-label={`Edit price alert ${a.label||a.symbol} at ${a.price}`} title={`${a.label||a.symbol} · ${a.state} · click to edit, drag to preview`} onPointerDown={e=>down(e,a)}
      onClick={e=>{if(suppressClick.current){suppressClick.current=false;return;}const ids=e.currentTarget.parentElement?.dataset.ids?.split(",")??[];if(ids.length>1)setChoices(alerts.filter(a=>ids.includes(a.id)));else if(!edit)open(a);}}
      style={{display:"flex",gap:4,alignItems:"center",height:"100%",background:"transparent",border:0,color:"inherit",font:"11px monospace",cursor:"ns-resize"}}><IconBell size={12}/>{a.state==="paused"&&<span aria-label="Paused">Ⅱ</span>}<span data-alert-text>{a.price}</span></button>
    </div>
   </div>)}
  </div>
  {props.armed&&<div data-drawing-ui="true" style={{position:"absolute",left:12,bottom:12,zIndex:9,pointerEvents:"none",background:"#191b20",color:"#e9b55e",padding:6}}>Click a price to create an alert · Esc to cancel</div>}
  {choices&&<div data-drawing-ui="true" className="popover" role="dialog" aria-label="Choose price alert" style={{position:"absolute",right:props.rightAxisWidth+6,top:40,zIndex:15,padding:8,display:"grid",gap:5}}>{choices.map(a=><button key={a.id} onClick={()=>open(a)}>{a.label||a.symbol} · {a.condition==="at-or-above"?"≥":"≤"} {a.price}</button>)}<button onClick={()=>setChoices(null)}>Cancel</button></div>}
  {edit&&<PriceAlertEditor {...edit} commands={props.commands} onClose={()=>setEdit(null)}/>}
 </>;
}
