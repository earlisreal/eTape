import type { TrailConfig, LimitCushion } from "../../wire/contract";

export function TrailEditor({value:c,onChange,lockMode=false,lockSessions=false,cushionOnly=false,inheritCushion=false}: {
  value:TrailConfig;onChange(c:TrailConfig):void;lockMode?:boolean;lockSessions?:boolean;cushionOnly?:boolean;inheritCushion?:boolean;
}):JSX.Element {
  const amount=(label:string,key:"buffer"|"cushion"|"offset")=><label>{label} <input aria-label={label} className="field" type="number" step="any" min={key==="offset"?undefined:0}
    value={inheritCushion && key==="cushion" && c[key].unit===""?"":c[key].value} placeholder={inheritCushion && key==="cushion"?"inherit":undefined} onChange={e=>onChange({...c,[key]:{value:Number(e.target.value),unit:c[key].unit||"$"}})} style={{width:78}}/>
    <select aria-label={`${label} unit`} className="field" value={c[key].unit||"$"} onChange={e=>onChange({...c,[key]:{...c[key],unit:e.target.value} as LimitCushion})}><option>$</option><option>%</option></select></label>;
  return <fieldset style={{display:"flex",flexWrap:"wrap",gap:6,border:"1px solid currentColor",padding:6}}><legend>Trailing stop</legend>
    <label>Mode <select aria-label="Trail mode" className="field" value={c.mode} disabled={lockMode} onChange={e=>onChange({...c,mode:e.target.value})}>
      <option value="PRICE">Trail Price</option><option value="PERCENT">Trail Percent</option><option value="CANDLE">Candle Trail</option></select></label>
    {c.mode==="CANDLE"?<><label>Timeframe <select aria-label="Trail timeframe" className="field" value={c.timeframe??"1m"} onChange={e=>onChange({...c,timeframe:e.target.value})}>
      {["10s","1m","5m","15m","30m","60m"].map(tf=><option key={tf}>{tf}</option>)}</select></label>{amount("Candle buffer","buffer")}</>:
      <label>Distance {c.mode==="PERCENT"?"%":"$"} <input aria-label="Trail distance" className="field" type="number" min="0" step="any" value={c.value||""} placeholder="required" onChange={e=>onChange({...c,value:Number(e.target.value)})} style={{width:85}}/></label>}
    {!lockSessions && <span>{(["pre","rth","post"] as const).map(s=><label key={s}><input type="checkbox" checked={c.sessions[s]} onChange={e=>onChange({...c,sessions:{...c.sessions,[s]:e.target.checked}})}/>{s.toUpperCase()} </label>)}</span>}
    {!cushionOnly && <label>Execution <select aria-label="Trail execution" className="field" value={c.pricing} onChange={e=>onChange({...c,pricing:e.target.value})}><option value="QUOTE">Quote Offset</option><option value="CUSHION">Limit Cushion</option></select></label>}
    {c.pricing==="QUOTE" && !cushionOnly?<><select aria-label="Trail quote side" className="field" value={c.quoteSide} onChange={e=>onChange({...c,quoteSide:e.target.value})}><option value="">Sell Bid / Cover Ask</option><option value="BID">Bid</option><option value="ASK">Ask</option></select>{amount("Quote offset","offset")}</>:amount("Trail limit cushion","cushion")}
    <small style={{flexBasis:"100%"}}>Held by eTape for this engine run. A trigger sends one LIMIT; an unfilled limit can leave exposure open.</small>
    {inheritCushion && <small>A blank cushion inherits the current protection's Sell Limit Cushion.</small>}
  </fieldset>;
}
