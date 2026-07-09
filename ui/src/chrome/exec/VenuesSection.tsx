// Venues & credentials editor. Edits are FILE-ONLY: SetVenueSetup rewrites
// config.toml and PutCredential/DeleteCredential rewrite credentials.json;
// nothing here arms a venue or changes the running gate — changes apply at the
// next engine restart (hence the restart banner).
//
// Credentials model: ONE opaque key per venue (no more shared/named credential
// picker). addVenue() mints a stable, user-invisible `key-<uuid8>` name into
// venue.credentials; renaming a venue's id never touches that name, so no
// orphaning happens on rename. Typed Key ID/Secret are write-only, tracked in
// local state keyed by that same opaque name (never seeded from `setup` —
// the engine never sends secrets back), sent once on Save, then cleared.
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import type { AckMsg, Venue, Gate, VenueConfig, VenueSetup } from "../../wire/contract";

interface Commands { sendCommand(name: string, args: unknown): Promise<AckMsg>; }

const BROKERS = ["tradezero", "alpaca", "moomoo", "sim"];
const ENVS = ["paper", "live"];
const BROKER_LABEL: Record<string, string> = { tradezero: "TradeZero", alpaca: "Alpaca", moomoo: "moomoo", sim: "Simulated" };
const VENUE_ID_RE = /^[a-z0-9-]+$/;
const CRED_REQUIRED_BROKERS = new Set(["tradezero", "alpaca"]);
const GATE_CAPS = ["maxOrderValue", "maxPositionValue", "maxPositionShares", "maxOpenOrders"] as const;
const GLOBAL_CAPS = ["maxDayLoss", "maxSymbolPositionValue", "maxSymbolPositionShares"] as const;

const emptyGate = (): Gate => ({ global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venue: {} });
const mintCredName = () => `key-${crypto.randomUUID().slice(0, 8)}`;

interface SecretDraft { keyId: string; secret: string }
interface VenueIssues { id?: string; account?: string; cred?: string }

export function VenuesSection({ commands }: { commands: Commands }): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const [setup, setSetup] = useState<VenueSetup | null>(null);
  const [draft, setDraft] = useState<VenueConfig>({ venues: [], gate: emptyGate() });
  const [err, setErr] = useState("");
  // Write-only typed secrets, keyed by the venue's opaque `credentials` name
  // (stable across id renames) — never populated from a refresh.
  const [secretDrafts, setSecretDrafts] = useState<Record<string, SecretDraft>>({});
  const [removeConfirmIdx, setRemoveConfirmIdx] = useState<number | null>(null);

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

  // Client-side mirror of the engine's SetVenueSetup validation (settings
  // redesign design §6) — surfaced pre-save so users see errors before a
  // round-trip, not instead of the engine's own authoritative checks (its
  // ack.reason still renders in venues-error on rejection).
  const validation = useMemo<VenueIssues[]>(() => {
    const idCounts = new Map<string, number>();
    draft.venues.forEach((v) => idCounts.set(v.id, (idCounts.get(v.id) ?? 0) + 1));
    return draft.venues.map((v) => {
      const issues: VenueIssues = {};
      if (!v.id) issues.id = "id is required";
      else if (!VENUE_ID_RE.test(v.id)) issues.id = "id must be lowercase letters, digits, and -";
      else if ((idCounts.get(v.id) ?? 0) > 1) issues.id = "id must be unique";

      const typed = secretDrafts[v.credentials];
      const typedKeyId = !!typed?.keyId;
      const typedSecret = !!typed?.secret;
      if (typedKeyId !== typedSecret) {
        issues.cred = "enter both key id and secret, or neither";
      } else if (CRED_REQUIRED_BROKERS.has(v.broker)) {
        const satisfied = (setup?.credKeys ?? []).includes(v.credentials) || (typedKeyId && typedSecret);
        if (!satisfied) issues.cred = `${BROKER_LABEL[v.broker] ?? v.broker} requires a credential key`;
      }

      if (v.broker === "tradezero" && !v.accountId) issues.account = "account id is required for TradeZero";
      return issues;
    });
  }, [draft.venues, secretDrafts, setup]);
  const hasErrors = validation.some((i) => Object.keys(i).length > 0);

  const patchVenue = (i: number, over: Partial<Venue>) =>
    setDraft((d) => ({ ...d, venues: d.venues.map((v, j) => (j === i ? { ...v, ...over } : v)) }));
  const setEnv = (i: number, env: string) =>
    patchVenue(i, { env, autoArm: env === "live" ? false : draft.venues[i].autoArm });
  const addVenue = () => setDraft((d) => ({
    ...d,
    venues: [...d.venues, { id: "", broker: "sim", env: "paper", credentials: mintCredName(), accountId: "", autoArm: false }],
  }));
  const removeVenue = (i: number) => {
    const gone = draft.venues[i];
    setDraft((d) => ({ ...d, venues: d.venues.filter((_, j) => j !== i) }));
    if (gone) setSecretDrafts((d) => {
      if (!(gone.credentials in d)) return d;
      const next = { ...d };
      delete next[gone.credentials];
      return next;
    });
  };
  const setSecretField = (credName: string, field: "keyId" | "secret", value: string) =>
    setSecretDrafts((d) => ({ ...d, [credName]: { keyId: d[credName]?.keyId ?? "", secret: d[credName]?.secret ?? "", [field]: value } }));

  const saveVenues = async () => {
    if (hasErrors) return; // Save is disabled in this state, but guard anyway
    setErr("");
    try {
      // 1. Push any newly-typed credentials first — SetVenueSetup requires
      //    tradezero/alpaca venues to name an EXISTING key, so the key must
      //    exist before the venue draft that references it is written.
      for (const v of draft.venues) {
        const typed = secretDrafts[v.credentials];
        if (!typed?.keyId || !typed?.secret) continue;
        const ack = await commands.sendCommand("PutCredential", { name: v.credentials, keyId: typed.keyId, secretKey: typed.secret });
        if (ack.status !== "accepted") { setErr(ack.reason || "rejected"); return; }
      }

      // 2. Write the venue/gate draft itself.
      const setAck = await commands.sendCommand("SetVenueSetup", { venues: draft.venues, gate: draft.gate });
      if (setAck.status !== "accepted") { setErr(setAck.reason || "rejected"); return; }

      // 3. Best-effort cleanup: credential names that belonged to venues in the
      //    previous file config but are no longer referenced by the saved
      //    draft (i.e. that venue was removed, not just renamed) are now
      //    unreferenced and can be deleted. Ignore failures — this is tidying,
      //    not correctness.
      const kept = new Set(draft.venues.map((v) => v.credentials).filter(Boolean));
      const oldNames = (setup?.file.venues ?? []).map((v) => v.credentials).filter(Boolean);
      for (const name of oldNames) {
        if (kept.has(name)) continue;
        try { await commands.sendCommand("DeleteCredential", { name }); } catch { /* best-effort */ }
      }

      // 4. Clear write-only inputs and reload.
      setSecretDrafts({});
      refresh();
    } catch {
      toast.push({ level: "danger", text: "Save failed (transport)." });
    }
  };

  const field = { className: "field" } as const; // spread onto native inputs/selects for shared look
  const groupLabel = { marginBottom: 4 } as const;
  const fieldWrap = { display: "flex", flexDirection: "column", gap: 2, fontSize: 10.5, color: palette.textMuted } as const;
  const issueText = { color: palette.danger, fontSize: 10, marginTop: 2 } as const;

  return (
    <div style={{ color: palette.text }}>
      {restartNeeded && (
        <div data-testid="restart-banner" style={{ background: palette.bg, border: `1px solid ${palette.accent}`, color: palette.accent, padding: "8px 12px", borderRadius: 4, marginBottom: 12, fontSize: 12 }}>
          ⚠ Engine restart required — saved venue config differs from the running engine.
        </div>
      )}
      {err && <div data-testid="venues-error" style={{ color: palette.danger, marginBottom: 8, fontSize: 12 }}>{err}</div>}

      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 8 }}>
        <div className="serif" style={{ fontSize: 14, fontWeight: 600 }}>Venues</div>
        <button data-testid="add-venue" className="btn" onClick={addVenue}>+ Add venue</button>
      </div>

      {draft.venues.map((v, i) => {
        const isLive = v.env === "live";
        const showCreds = v.broker !== "sim";
        const keySet = !!setup?.credKeys.includes(v.credentials);
        const issue = validation[i];
        const typed = secretDrafts[v.credentials] ?? { keyId: "", secret: "" };
        const removing = removeConfirmIdx === i;

        return (
          <div key={i} className="venue-card" style={{
            border: `1px solid ${palette.border}`, borderRadius: 6, background: palette.bg,
            marginBottom: 12, overflow: "hidden",
            borderTop: isLive ? `3px solid ${palette.danger}` : `1px solid ${palette.border}`,
          }}>
            <div className={isLive ? "venue-card-header-live" : undefined} style={{
              display: "flex", alignItems: "center", gap: 8, padding: "8px 12px",
              borderBottom: `1px solid ${palette.border}`,
            }}>
              <span className="mono" style={{ fontWeight: 600 }}>{v.id || "(unnamed)"}</span>
              <span style={{ color: palette.textMuted }}>{BROKER_LABEL[v.broker] ?? v.broker}</span>
              <span className={`chip ${isLive ? "chip-live" : ""}`} style={!isLive ? { color: palette.textMuted } : undefined}>
                {v.env.toUpperCase()}
              </span>
              <span style={{ flex: 1 }} />
              {removing && <span style={{ fontSize: 11, color: palette.danger, marginRight: 4 }}>Remove {v.id || "this venue"}?</span>}
              <button
                data-testid={`venue-remove-${i}`}
                className="btn venue-remove-btn"
                style={removing ? { borderColor: palette.danger, color: palette.danger } : undefined}
                onClick={() => (removing ? (removeVenue(i), setRemoveConfirmIdx(null)) : setRemoveConfirmIdx(i))}
              >
                {removing ? "Confirm remove" : "Remove"}
              </button>
              {removing && <button className="btn" onClick={() => setRemoveConfirmIdx(null)}>Cancel</button>}
            </div>

            <div style={{ padding: 12, display: "flex", flexDirection: "column", gap: 12 }}>
              <div>
                <div className="col-head" style={groupLabel}>Connection</div>
                <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                  <label style={fieldWrap}>
                    id
                    <input {...field} className="field mono" data-testid={`venue-id-${i}`} value={v.id}
                      onChange={(e) => patchVenue(i, { id: e.target.value })} placeholder="venue-id" style={{ width: 130 }} />
                  </label>
                  <label style={fieldWrap}>
                    broker
                    <select {...field} data-testid={`venue-broker-${i}`} value={v.broker}
                      onChange={(e) => patchVenue(i, { broker: e.target.value })} style={{ width: 100 }}>
                      {BROKERS.map((b) => <option key={b} value={b}>{BROKER_LABEL[b]}</option>)}
                    </select>
                  </label>
                  <label style={fieldWrap}>
                    env
                    <select {...field} data-testid={`venue-env-${i}`} value={v.env}
                      onChange={(e) => setEnv(i, e.target.value)} style={{ width: 80 }}>
                      {ENVS.map((x) => <option key={x} value={x}>{x}</option>)}
                    </select>
                  </label>
                  <label style={fieldWrap}>
                    account id
                    <input {...field} data-testid={`venue-account-${i}`} value={v.accountId}
                      onChange={(e) => patchVenue(i, { accountId: e.target.value })} placeholder="account id" style={{ width: 110 }} />
                  </label>
                </div>
                {(issue.id || issue.account) && (
                  <div style={issueText}>{[issue.id, issue.account].filter(Boolean).join(" · ")}</div>
                )}
              </div>

              {showCreds && (
                <div>
                  <div className="col-head" style={groupLabel}>Credentials</div>
                  <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "flex-end" }}>
                    <label style={fieldWrap}>
                      key id
                      <input {...field} type="password" autoComplete="off" data-testid={`venue-cred-keyid-${i}`}
                        value={typed.keyId} onChange={(e) => setSecretField(v.credentials, "keyId", e.target.value)}
                        placeholder="•••• (masked)" style={{ width: 150 }} />
                    </label>
                    <label style={fieldWrap}>
                      secret
                      <input {...field} type="password" autoComplete="off" data-testid={`venue-cred-secret-${i}`}
                        value={typed.secret} onChange={(e) => setSecretField(v.credentials, "secret", e.target.value)}
                        placeholder="•••• (masked)" style={{ width: 180 }} />
                    </label>
                    <span className={`chip ${keySet ? "chip-set" : ""}`} style={!keySet ? { color: palette.textMuted } : undefined}>
                      {keySet ? "key set" : "no key"}
                    </span>
                  </div>
                  <div style={{ color: palette.textMuted, fontSize: 10, marginTop: 4 }}>leave blank to keep the existing key</div>
                  {issue.cred && <div style={issueText}>{issue.cred}</div>}
                </div>
              )}

              <div>
                <div className="col-head" style={groupLabel}>Risk limits</div>
                <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "flex-end" }}>
                  {GATE_CAPS.map((cap) => (
                    <label key={cap} style={fieldWrap}>
                      {cap}
                      <input {...field} className="field mono" value={String(draft.gate.venue[v.id]?.[cap] ?? 0)}
                        onChange={(e) => setDraft((d) => {
                          const cur = d.gate.venue[v.id] ?? { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 };
                          return { ...d, gate: { ...d.gate, venue: { ...d.gate.venue, [v.id]: { ...cur, [cap]: Number(e.target.value) || 0 } } } };
                        })}
                        style={{ width: 72 }} />
                    </label>
                  ))}
                  <label style={{ ...fieldWrap, flexDirection: "row", alignItems: "center", gap: 4, opacity: isLive ? 0.6 : 1 }}>
                    <input data-testid={`venue-autoarm-${i}`} type="checkbox" disabled={isLive}
                      checked={isLive ? false : v.autoArm} onChange={(e) => patchVenue(i, { autoArm: e.target.checked })} />
                    auto-arm
                  </label>
                </div>
              </div>
            </div>
          </div>
        );
      })}

      <div style={{ marginTop: 16, marginBottom: 16 }}>
        <div className="serif" style={{ fontSize: 14, fontWeight: 600, marginBottom: 8 }}>Global limits</div>
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
          {GLOBAL_CAPS.map((k) => (
            <label key={k} style={fieldWrap}>
              {k}
              <input {...field} className="field mono" data-testid={`global-${k}`} value={String(draft.gate.global[k])}
                onChange={(e) => setDraft((d) => ({ ...d, gate: { ...d.gate, global: { ...d.gate.global, [k]: Number(e.target.value) || 0 } } }))}
                style={{ width: 90 }} />
            </label>
          ))}
        </div>
      </div>

      <div style={{ display: "flex", justifyContent: "flex-end" }}>
        <button data-testid="save-venues" className="btn btn-primary" disabled={hasErrors} onClick={() => void saveVenues()}
          style={hasErrors ? { opacity: 0.5, cursor: "not-allowed" } : undefined}>
          Save venues & limits
        </button>
      </div>
    </div>
  );
}
