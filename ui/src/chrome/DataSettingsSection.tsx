import { useCallback, useEffect, useRef, useState, useSyncExternalStore, type FormEvent } from "react";
import { Button } from "./controls/Button";
import type { HealthStore } from "../data/HealthStore";
import type { SessionStore } from "../data/SessionStore";
import type { ConnState } from "../wire/WsClient";
import type { AckMsg, DataSettings, DataSettingsSetup } from "../wire/contract";

type Draft = {
  openDHost: string; openDPort: string; quotaSlots: string;
  premarketSeconds: string; rthSeconds: string; yahooNews: boolean;
  tenSecondDays: string; intradayDays: string; dailyYears: string; barRetentionDays: string;
  recordingEnabled: boolean; recordingRetentionDays: string; recordingMaxGiB: string; recordingMinFreeGiB: string;
};
type NumberKey = Exclude<keyof Draft, "openDHost" | "yahooNews" | "recordingEnabled">;
const GIB = 1024 ** 3;

function toDraft(s: DataSettings): Draft {
  return {
    openDHost: s.openDHost, openDPort: String(s.openDPort), quotaSlots: String(s.quotaSlots),
    premarketSeconds: String(s.scannerPremarketMs / 1000), rthSeconds: String(s.scannerRTHMs / 1000), yahooNews: s.yahooNews,
    tenSecondDays: String(s.tenSecondDays), intradayDays: String(s.intradayDays), dailyYears: String(s.dailyYears),
    barRetentionDays: String(s.barRetentionDays), recordingEnabled: s.recordingEnabled,
    recordingRetentionDays: String(s.recordingRetentionDays), recordingMaxGiB: String(s.recordingMaxBytes / GIB),
    recordingMinFreeGiB: String(s.recordingMinFreeBytes / GIB),
  };
}

function toSettings(d: Draft): DataSettings {
  const integer = (value: string, name: string, scale = 1): number => {
    if (value.trim() === "") throw new Error(`${name} is required.`);
    const n = Number(value) * scale;
    const rounded = Math.round(n);
    if (!Number.isFinite(n) || !Number.isSafeInteger(rounded) || (scale === 1 && n !== rounded)) {
      throw new Error(`${name} must be a valid number.`);
    }
    return rounded;
  };
  return {
    openDHost: d.openDHost.trim(), openDPort: integer(d.openDPort, "OpenD port"), quotaSlots: integer(d.quotaSlots, "Subscription budget"),
    scannerPremarketMs: integer(d.premarketSeconds, "Premarket refresh", 1000), scannerRTHMs: integer(d.rthSeconds, "Regular-session refresh", 1000),
    yahooNews: d.yahooNews, tenSecondDays: integer(d.tenSecondDays, "10-second history"), intradayDays: integer(d.intradayDays, "1-minute history"),
    dailyYears: integer(d.dailyYears, "Daily history"), barRetentionDays: integer(d.barRetentionDays, "Bar retention"),
    recordingEnabled: d.recordingEnabled, recordingRetentionDays: integer(d.recordingRetentionDays, "Recording retention"),
    recordingMaxBytes: integer(d.recordingMaxGiB, "Recording cap", GIB), recordingMinFreeBytes: integer(d.recordingMinFreeGiB, "Free-space reserve", GIB),
  };
}

export function DataSettingsSection({ commands, engineState, session, health }: {
  commands: { sendCommand(name: string, args: unknown): Promise<AckMsg> };
  engineState?: ConnState | undefined;
  session?: SessionStore | undefined;
  health?: HealthStore | undefined;
}): JSX.Element {
  const mode = useSyncExternalStore((cb) => session?.subscribe(cb) ?? (() => {}), () => session?.getSnapshot().mode);
  const healthState = useSyncExternalStore((cb) => health?.subscribe(cb) ?? (() => {}), () => health?.getSnapshot());
  const [setup, setSetup] = useState<DataSettingsSetup | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [saved, setSaved] = useState(false);
  const sawDrop = useRef(false);

  const reload = useCallback(async () => {
    setError("");
    try {
      const ack = await commands.sendCommand("GetDataSettings", {});
      if (ack.status !== "accepted" || !ack.value) throw new Error(ack.reason || "Could not load data settings.");
      const value = ack.value as DataSettingsSetup;
      setSetup(value);
      setDraft(toDraft(value.file));
      setSaved(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not load data settings.");
    }
  }, [commands]);

  useEffect(() => { void reload(); }, [reload]);
  useEffect(() => {
    if (!restarting) { sawDrop.current = false; return; }
    if (engineState !== "open") { sawDrop.current = true; return; }
    if (sawDrop.current) window.location.reload();
  }, [engineState, restarting]);

  const update = (key: keyof Draft, value: string | boolean) => {
    setDraft((current) => current ? { ...current, [key]: value } : current);
    setSaved(false);
  };
  const dirty = !!draft && !!setup && JSON.stringify(draft) !== JSON.stringify(toDraft(setup.file));
  const restartNeeded = mode !== "demo" && !!setup && JSON.stringify(setup.file) !== JSON.stringify(setup.running);

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (!draft || !setup) return;
    setError("");
    setSaving(true);
    try {
      const settings = toSettings(draft);
      const ack = await commands.sendCommand("SetDataSettings", { expected: setup.file, settings });
      if (ack.status !== "accepted") throw new Error(ack.reason || "Could not save data settings.");
      setSetup({ ...setup, file: settings });
      setDraft(toDraft(settings));
      setSaved(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save data settings.");
    } finally {
      setSaving(false);
    }
  };

  const restart = async () => {
    setRestarting(true);
    try {
      const ack = await commands.sendCommand("RestartEngine", {});
      if (ack.status !== "accepted") throw new Error(ack.reason || "Restart rejected.");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Restart failed.");
      setRestarting(false);
    }
  };

  const numberField = (key: NumberKey, label: string, min: number, step = "1", disabled = false) => (
    <label style={{ display: "grid", gap: 5, fontSize: 12 }}>
      {label}
      <input className="field mono" type="number" min={min} step={step} required disabled={disabled}
        value={draft?.[key] ?? ""} onChange={(e) => update(key, e.target.value)} />
    </label>
  );
  const grid = { display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(165px, 1fr))", gap: 12 };
  const note = { fontSize: 12, color: "var(--text-muted)", lineHeight: 1.5, margin: "8px 0 0" };

  if (!setup || !draft) return <div>{error ? <><p role="alert">{error}</p><Button onClick={() => void reload()}>Retry</Button></> : "Loading settings…"}</div>;
  return (
    <form onSubmit={(e) => void save(e)}>
      {mode === "demo" && <p style={note}>Demo uses built-in defaults. Changes apply the next time live mode starts.</p>}
      {restartNeeded && <div role="status" style={{ ...note, display: "flex", alignItems: "center", gap: 10, marginBottom: 14 }}>
        <span>Restart the engine to apply saved data settings.</span>
        <Button type="button" variant="danger" confirm confirmLabel="Confirm restart" loading={restarting} onClick={() => void restart()}>Restart now</Button>
      </div>}
      {error && <div role="alert" style={{ color: "var(--danger)", marginBottom: 12 }}>{error} <Button type="button" onClick={() => void reload()}>Reload</Button></div>}

      <div className="settings-group">
        <div className="col-head serif" style={{ marginBottom: 10 }}>Chart history</div>
        <div style={grid}>
          {numberField("tenSecondDays", "10-second history (days)", 0)}
          {numberField("intradayDays", "1-minute history (days)", 0)}
          {numberField("dailyYears", "Initial daily history (years)", 0)}
          {numberField("barRetentionDays", "10-second bar retention (days)", 0)}
        </div>
        <p style={note}>Days are calendar days. For 10-second history, 0 means the current trading cycle; for 1-minute history, 0 uses 70 days; for daily history, 0 starts in 2016. Bar retention 0 disables pruning.</p>
        <p style={note}>Reducing bar retention can delete older bars on the next start. Loading more history cannot restore pruned 10-second bars.</p>
      </div>

      <div className="settings-group">
        <div className="col-head serif" style={{ marginBottom: 10 }}>Scanner refresh</div>
        <div style={grid}>
          {numberField("premarketSeconds", "Extended-hours refresh (seconds)", 0.001, "0.001")}
          {numberField("rthSeconds", "Regular-session refresh (seconds)", 0.001, "0.001")}
        </div>
        <p style={note}>Actual refresh depends on provider response times and request limits.</p>
      </div>

      <div className="settings-group">
        <div className="col-head serif" style={{ marginBottom: 10 }}>News</div>
        <label><input type="checkbox" checked={draft.yahooNews} onChange={(e) => update("yahooNews", e.target.checked)} /> Use experimental Yahoo headline supplement</label>
      </div>

      <div className="settings-group">
        <div className="col-head serif" style={{ marginBottom: 10 }}>Tick recording</div>
        <label><input type="checkbox" checked={draft.recordingEnabled} onChange={(e) => update("recordingEnabled", e.target.checked)} /> Record market-data evidence</label>
        <div style={{ ...grid, marginTop: 10 }}>
          {numberField("recordingRetentionDays", "Keep up to (days)", 1, "1", !draft.recordingEnabled)}
          {numberField("recordingMaxGiB", "Archive limit (GiB)", 0, "any", !draft.recordingEnabled)}
          {numberField("recordingMinFreeGiB", "Free-space reserve (GiB)", 0, "any", !draft.recordingEnabled)}
        </div>
        <p style={note}>The size limit may shorten retention. Reducing limits can remove older recordings. Stopping recording keeps existing captures but stops adding new evidence for Volume Profile.</p>
      </div>

      <div className="settings-group">
        <div className="col-head serif" style={{ marginBottom: 10 }}>OpenD connection</div>
        <div style={grid}>
          <label style={{ display: "grid", gap: 5, fontSize: 12 }}>Host<input className="field mono" required value={draft.openDHost} onChange={(e) => update("openDHost", e.target.value)} /></label>
          {numberField("openDPort", "Port", 1)}
          {numberField("quotaSlots", "eTape subscription budget", 1)}
        </div>
        <p style={note}>Keep the budget at or below your OpenD entitlement. {healthState?.quota
          ? `Account subscriptions: ${healthState.quota.subUsed} / ${healthState.quota.subUsed + healthState.quota.subRemain} used (eTape ${healthState.quota.subOwn}, other clients ${healthState.quota.subForeign}).`
          : "Account-wide quota is unavailable until OpenD reports it."}</p>
      </div>

      <div className="settings-save" style={{ display: "flex", alignItems: "center", gap: 10 }}>
        <Button type="submit" variant="primary" size="md" disabled={!dirty} loading={saving}>Save data settings</Button>
        {saved && <span role="status">Saved.</span>}
      </div>
    </form>
  );
}
