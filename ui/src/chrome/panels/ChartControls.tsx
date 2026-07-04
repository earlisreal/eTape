import { INDICATOR_CATALOG, describeIndicator, type IndicatorInstance, type IndicatorType } from "../../render/chart/indicatorSeries";
import type { Palette } from "../../render/palette";

const TFS: string[] = ["10s", "1m", "5m", "15m", "30m", "60m", "D", "W", "M"];
const TYPES = Object.keys(INDICATOR_CATALOG) as IndicatorType[];

export function ChartControls({ timeframe, instances, palette, onTimeframe, onAdd, onUpdate, onRemove }: {
  timeframe: string; instances: IndicatorInstance[]; palette: Palette;
  onTimeframe: (tf: string) => void; onAdd: (t: IndicatorType) => void;
  onUpdate: (i: IndicatorInstance) => void; onRemove: (id: string) => void;
}): JSX.Element {
  return (
    <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 6, padding: "3px 6px",
      background: palette.surface, borderBottom: `1px solid ${palette.border}`, color: palette.text, fontSize: 12 }}>
      <select aria-label="timeframe" value={timeframe} onChange={(e) => onTimeframe(e.target.value)}>
        {TFS.map((tf) => <option key={tf} value={tf}>{tf}</option>)}
      </select>
      <select aria-label="add indicator" value="" onChange={(e) => { if (e.target.value) onAdd(e.target.value as IndicatorType); }}>
        <option value="">+ indicator</option>
        {TYPES.map((t) => <option key={t} value={t}>{INDICATOR_CATALOG[t].label}</option>)}
      </select>
      {instances.map((inst) => <InstanceChip key={inst.instanceId} inst={inst} palette={palette} onUpdate={onUpdate} onRemove={onRemove} />)}
    </div>
  );
}

function InstanceChip({ inst, palette, onUpdate, onRemove }: {
  inst: IndicatorInstance; palette: Palette; onUpdate: (i: IndicatorInstance) => void; onRemove: (id: string) => void;
}): JSX.Element {
  const entry = INDICATOR_CATALOG[inst.type];
  const setParam = (k: string, v: number) => onUpdate({ ...inst, params: { ...inst.params, [k]: v } });
  const setColor = (slot: string, c: string) => onUpdate({ ...inst, colors: { ...inst.colors, [slot]: c } });
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 4, padding: "1px 5px",
      border: `1px solid ${palette.border}`, borderRadius: 3 }}>
      <b style={{ fontWeight: 600 }}>{entry.label}</b>
      {entry.params.map((p) => (
        <label key={p.key} style={{ color: palette.textMuted }}>{p.label[0]}
          <input type="number" min={p.min} max={p.max} value={inst.params[p.key] ?? p.default}
            onChange={(e) => setParam(p.key, Number(e.target.value))} style={{ width: 42, marginLeft: 2 }} />
        </label>
      ))}
      {describeIndicator(inst, palette).map((d) => (
        <input key={d.slot} aria-label={`${inst.instanceId} ${d.slot} color`} type="color" value={d.color}
          onChange={(e) => setColor(d.slot, e.target.value)} style={{ width: 18, height: 16, padding: 0, border: "none", background: "none" }} />
      ))}
      <button aria-label={`remove ${inst.instanceId}`} onClick={() => onRemove(inst.instanceId)}
        style={{ color: palette.textMuted, border: "none", background: "none", cursor: "pointer" }}>×</button>
    </span>
  );
}
