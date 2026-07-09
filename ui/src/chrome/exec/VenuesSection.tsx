// Venues & credentials editor. Edits are FILE-ONLY: SetVenueSetup rewrites
// config.toml and credential ops rewrite credentials.json; nothing here arms a
// venue or changes the running gate — changes apply at the next engine restart
// (hence the restart banner). Secrets are write-only: keyId/secretKey are typed
// here, sent once on save, and never read back from the engine.
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { HoverButton } from "../controls/HoverButton";
import type { AckMsg, Venue, Gate, VenueConfig, VenueSetup } from "../../wire/contract";

interface Commands { sendCommand(name: string, args: unknown): Promise<AckMsg>; }
const BROKERS = ["tradezero", "alpaca", "moomoo", "sim"];
const ENVS = ["paper", "live"];
const emptyGate = (): Gate => ({ global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venue: {} });

export function VenuesSection({ commands }: { commands: Commands }): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const [setup, setSetup] = useState<VenueSetup | null>(null);
  const [draft, setDraft] = useState<VenueConfig>({ venues: [], gate: emptyGate() });
  const [err, setErr] = useState("");
  const [credName, setCredName] = useState("");
  const [credKeyId, setCredKeyId] = useState("");
  const [credSecret, setCredSecret] = useState("");

  const refresh = useCallback(() => {
    void commands.sendCommand("GetVenueSetup", {}).then((ack) => {
      if (ack.status === "accepted" && ack.value) {
        const s = ack.value as VenueSetup;
        setSetup(s);
        setDraft({ venues: s.file.venues.map((v) => ({ ...v })), gate: { global: { ...s.file.gate.global }, venue: { ...s.file.gate.venue } } });
      }
    }).catch(() => toast.push({ level: "danger", text: "Could not load venue setup." }));
  }, [commands, toast]);
  useEffect(refresh, [refresh]);

  const restartNeeded = useMemo(
    () => setup !== null && JSON.stringify(setup.file) !== JSON.stringify(setup.running),
    [setup],
  );
  const usedBy = (key: string) => draft.venues.filter((v) => v.credentials === key).map((v) => v.id);

  const patchVenue = (i: number, over: Partial<Venue>) =>
    setDraft((d) => ({ ...d, venues: d.venues.map((v, j) => (j === i ? { ...v, ...over } : v)) }));
  const addVenue = () => setDraft((d) => ({ ...d, venues: [...d.venues, { id: "", broker: "sim", env: "paper", credentials: "", accountId: "", autoArm: false }] }));
  const removeVenue = (i: number) => setDraft((d) => ({ ...d, venues: d.venues.filter((_, j) => j !== i) }));

  const saveVenues = () => {
    setErr("");
    void commands.sendCommand("SetVenueSetup", { venues: draft.venues, gate: draft.gate }).then((ack) => {
      if (ack.status !== "accepted") { setErr(ack.reason || "rejected"); return; }
      refresh();
    }).catch(() => toast.push({ level: "danger", text: "Save failed (transport)." }));
  };
  const putCred = () => {
    void commands.sendCommand("PutCredential", { name: credName, keyId: credKeyId, secretKey: credSecret }).then((ack) => {
      if (ack.status !== "accepted") { setErr(ack.reason || "rejected"); return; }
      setCredName(""); setCredKeyId(""); setCredSecret(""); // clear write-only inputs
      refresh();
    }).catch(() => toast.push({ level: "danger", text: "Credential save failed (transport)." }));
  };
  const delCred = (name: string) => {
    void commands.sendCommand("DeleteCredential", { name }).then((ack) => {
      if (ack.status !== "accepted") { setErr(ack.reason || "rejected"); return; }
      refresh();
    }).catch(() => toast.push({ level: "danger", text: "Credential delete failed (transport)." }));
  };

  const inp = { background: palette.bg, color: palette.text, border: `1px solid ${palette.border}`, fontSize: 12, padding: "1px 4px" } as const;
  const badge = (env: string) => ({ padding: "0 6px", borderRadius: 3, fontSize: 10, fontWeight: 700, color: env === "live" ? palette.danger : palette.textMuted, border: `1px solid ${env === "live" ? palette.danger : palette.border}` });

  return (
    <div style={{ color: palette.text }}>
      {restartNeeded && (
        <div data-testid="restart-banner" style={{ background: palette.bg, border: `1px solid ${palette.accent}`, color: palette.accent, padding: "6px 8px", borderRadius: 4, marginBottom: 10 }}>
          ⚠ Engine restart required — saved venue config differs from the running engine.
        </div>
      )}
      {err && <div data-testid="venues-error" style={{ color: palette.danger, marginBottom: 8 }}>{err}</div>}

      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <div style={{ fontWeight: 700 }}>Venues</div>
        <HoverButton data-testid="add-venue" onClick={addVenue} style={{ ...inp, cursor: "pointer" }}>+ Add venue</HoverButton>
      </div>

      {draft.venues.map((v, i) => (
        <div key={i} style={{ borderTop: `1px solid ${palette.border}`, padding: "4px 0" }}>
          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <input data-testid={`venue-id-${i}`} value={v.id} onChange={(e) => patchVenue(i, { id: e.target.value })} placeholder="id" style={{ ...inp, width: 120 }} className="mono" />
            <select data-testid={`venue-broker-${i}`} value={v.broker} onChange={(e) => patchVenue(i, { broker: e.target.value })} style={inp}>{BROKERS.map((b) => <option key={b}>{b}</option>)}</select>
            <select data-testid={`venue-env-${i}`} value={v.env} onChange={(e) => patchVenue(i, { env: e.target.value, autoArm: e.target.value === "live" ? false : v.autoArm })} style={inp}>{ENVS.map((x) => <option key={x}>{x}</option>)}</select>
            <span style={badge(v.env)}>{v.env.toUpperCase()}</span>
            <select data-testid={`venue-cred-${i}`} value={v.credentials} onChange={(e) => patchVenue(i, { credentials: e.target.value })} style={inp}>
              <option value="">—</option>{(setup?.credKeys ?? []).map((k) => <option key={k}>{k}</option>)}
            </select>
            <input data-testid={`venue-account-${i}`} value={v.accountId} onChange={(e) => patchVenue(i, { accountId: e.target.value })} placeholder="account id" style={{ ...inp, width: 100 }} />
            <label style={{ display: "flex", gap: 4, alignItems: "center", opacity: v.env === "live" ? 0.5 : 1 }}>
              <input data-testid={`venue-autoarm-${i}`} type="checkbox" disabled={v.env === "live"} checked={v.autoArm} onChange={(e) => patchVenue(i, { autoArm: e.target.checked })} />
              auto-arm
            </label>
            <HoverButton data-testid={`venue-remove-${i}`} onClick={() => removeVenue(i)} style={{ ...inp, color: palette.danger, cursor: "pointer" }}>remove</HoverButton>
          </div>
          <div style={{ display: "flex", gap: 8, marginTop: 3 }}>
            {(["maxOrderValue", "maxPositionValue", "maxPositionShares", "maxOpenOrders"] as const).map((cap) => (
              <label key={cap} style={{ fontSize: 11, color: palette.textMuted }}>{cap}{" "}
                <input value={String(draft.gate.venue[v.id]?.[cap] ?? 0)} onChange={(e) => setDraft((d) => {
                  const cur = d.gate.venue[v.id] ?? { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 };
                  return { ...d, gate: { ...d.gate, venue: { ...d.gate.venue, [v.id]: { ...cur, [cap]: Number(e.target.value) || 0 } } } };
                })} style={{ ...inp, width: 70 }} />
              </label>
            ))}
          </div>
        </div>
      ))}

      <div style={{ marginTop: 8, display: "flex", gap: 8 }}>
        <span style={{ fontWeight: 700 }}>Global limits</span>
        {(["maxDayLoss", "maxSymbolPositionValue", "maxSymbolPositionShares"] as const).map((k) => (
          <label key={k} style={{ fontSize: 11, color: palette.textMuted }}>{k}{" "}
            <input data-testid={`global-${k}`} value={String(draft.gate.global[k])} onChange={(e) => setDraft((d) => ({ ...d, gate: { ...d.gate, global: { ...d.gate.global, [k]: Number(e.target.value) || 0 } } }))} style={{ ...inp, width: 80 }} />
          </label>
        ))}
      </div>

      <div style={{ marginTop: 14, display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <div style={{ fontWeight: 700 }}>Credentials</div>
      </div>
      {(setup?.credKeys ?? []).map((k) => {
        const refs = usedBy(k);
        return (
          <div key={k} style={{ display: "flex", gap: 10, alignItems: "center", borderTop: `1px solid ${palette.border}`, padding: "3px 0" }}>
            <span className="mono" style={{ width: 140 }}>{k}</span>
            <span style={{ color: palette.textMuted, flex: 1 }}>used by: {refs.length ? refs.join(", ") : "—"}</span>
            <HoverButton data-testid={`credential-delete-${k}`} disabled={refs.length > 0} onClick={() => delCred(k)} style={{ ...inp, color: palette.danger, cursor: refs.length ? "not-allowed" : "pointer" }}>delete</HoverButton>
          </div>
        );
      })}
      <div style={{ display: "flex", gap: 6, marginTop: 6, alignItems: "center" }}>
        <input data-testid="cred-name" value={credName} onChange={(e) => setCredName(e.target.value)} placeholder="name" style={{ ...inp, width: 120 }} />
        <input data-testid="cred-keyid" value={credKeyId} onChange={(e) => setCredKeyId(e.target.value)} placeholder="key id" type="password" style={{ ...inp, width: 140 }} />
        <input data-testid="cred-secret" value={credSecret} onChange={(e) => setCredSecret(e.target.value)} placeholder="secret key" type="password" style={{ ...inp, width: 180 }} />
        <HoverButton data-testid="cred-save" onClick={putCred} style={{ ...inp, cursor: "pointer" }}>Add / replace key</HoverButton>
      </div>

      <div style={{ display: "flex", justifyContent: "flex-end", marginTop: 14 }}>
        <HoverButton data-testid="save-venues" onClick={saveVenues} style={{ ...inp, background: palette.accent, color: palette.bg, fontWeight: 700, cursor: "pointer" }}>Save venues & limits</HoverButton>
      </div>
    </div>
  );
}
