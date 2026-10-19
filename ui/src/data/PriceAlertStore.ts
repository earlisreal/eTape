import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, PriceAlertState, PriceAlertNotification } from "../wire/contract";

export class PriceAlertStore extends ReactStore<PriceAlertState> {
 private listeners=new Set<(n:PriceAlertNotification)=>void>();
 constructor(){super({alerts:[],history:[],historyTotal:0,revision:0,error:""});}
 apply(m:SnapshotMsg|DeltaMsg):void {
  if(m.topic==="alerts.state") {
   const next=m.payload as PriceAlertState;
   // A reconnect starts a new engine epoch; its initial snapshot may restart revision at zero.
   if(m.kind==="snapshot"||next.revision>=this.getSnapshot().revision)this.set({...next,alerts:next.alerts??[],history:next.history??[]});
  }else if(m.topic==="alerts.trigger"&&m.kind==="delta") {
   this.listeners.forEach(fn=>fn(m.payload as PriceAlertNotification));
  }
 }
 onNotification(fn:(n:PriceAlertNotification)=>void):()=>void {this.listeners.add(fn);return()=>{this.listeners.delete(fn);};}
}
