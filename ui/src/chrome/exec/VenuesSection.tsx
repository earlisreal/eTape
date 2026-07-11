// Venues & credentials editor. Edits are FILE-ONLY: SetVenueSetup rewrites
// config.toml and PutCredential/DeleteCredential rewrite credentials.json;
// nothing here arms a venue or changes the running gate — changes apply at the
// next engine restart (hence the restart banner). ONE deliberate exception:
// "Reset balance" on a sim venue sends a live ResetBalance command straight to
// the running exec.Core (the same channel Flatten/Arm elsewhere use) rather
// than writing config — funding/resetting a sim account is inherently a live
// action, not a file edit, and the button only ever appears for a venue
// that's already running as broker "sim".
//
// Credentials model: ONE opaque key per venue (no more shared/named credential
// picker). addVenue() mints a stable, user-invisible `key-<uuid8>` name into
// venue.credentials; renaming a venue's id never touches that name, so no
// orphaning happens on rename. Typed Key ID/Secret are write-only, tracked in
// local state keyed by that same opaque name (never seeded from `setup` —
// the engine never sends secrets back), sent once on Save, then cleared.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import type { AckMsg, Venue, Gate, GateLimitsView, VenueConfig, VenueSetup, TestConnectionResult, TestAccount } from "../../wire/contract";
import type { ConnState } from "../../wire/WsClient";

interface Commands { sendCommand(name: string, args: unknown): Promise<AckMsg>; }

const BROKERS = ["tradezero", "alpaca", "moomoo", "sim"];
const ENVS = ["paper", "live"];
const BROKER_LABEL: Record<string, string> = { tradezero: "TradeZero", alpaca: "Alpaca", moomoo: "moomoo", sim: "Simulated" };
const VENUE_ID_RE = /^[a-z0-9-]+$/;
const CRED_REQUIRED_BROKERS = new Set(["tradezero", "alpaca"]);
// Brokers whose Test-connection probe can auto-detect env (and, for
// tradezero, the account id) — so their manual env/account-id inputs are
// replaced by a read-only detected display, and Save requires a passing Test
// before a freshly-typed (or never-yet-saved) key can be trusted.
const TESTABLE_BROKERS = new Set(["tradezero", "alpaca"]);
const GATE_CAPS = ["maxOrderValue", "maxPositionValue", "maxPositionShares", "maxOpenOrders"] as const;
const GLOBAL_CAPS = ["maxDayLoss", "maxSymbolPositionValue", "maxSymbolPositionShares"] as const;

const emptyGate = (): Gate => ({ global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venue: {} });
const mintCredName = () => `key-${crypto.randomUUID().slice(0, 8)}`;
const zeroCaps = (): GateLimitsView => ({ maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 });

interface SecretDraft { keyId: string; secret: string }
interface VenueIssues { id?: string; account?: string; cred?: string }
type TestStatus = "idle" | "testing" | "ok" | "fail";
interface TestState { status: TestStatus; message?: string; accounts?: TestAccount[] }

export function VenuesSection({ commands, engineState }: { commands: Commands; engineState?: ConnState | undefined }): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const [setup, setSetup] = useState<VenueSetup | null>(null);
  const [draft, setDraft] = useState<VenueConfig>({ venues: [], gate: emptyGate() });
  const [err, setErr] = useState("");
  // Write-only typed secrets, keyed by the venue's opaque `credentials` name
  // (stable across id renames) — never populated from a refresh.
  const [secretDrafts, setSecretDrafts] = useState<Record<string, SecretDraft>>({});
  const [removeConfirmIdx, setRemoveConfirmIdx] = useState<number | null>(null);
  const [resetConfirmIdx, setResetConfirmIdx] = useState<number | null>(null);
  const [restartConfirm, setRestartConfirm] = useState(false);
  const [restarting, setRestarting] = useState(false);
  // Stable per-row identity for risk-limit caps, independent of the venue's
  // mutable `id` field. gate.venue is keyed by id on the wire, but tracking
  // caps live-keyed-by-id during editing let two rows transiently share an
  // id mid-rename (before the uniqueness validation blocks Save) — two
  // review rounds each found a real caps-corruption bug in an id-keyed
  // migration scheme. Keying caps by a synthetic per-row id sidesteps the
  // whole class: renaming a venue's `id` field never touches its caps: they
  // are only ever projected into the wire's id-keyed Gate.venue shape at
  // Save time (see saveVenues).
  const [rowKeys, setRowKeys] = useState<string[]>([]);
  const [capsByRow, setCapsByRow] = useState<Record<string, GateLimitsView>>({});
  // Test-connection outcome per row, keyed by the same stable rowKeys as
  // capsByRow (not by v.id, which is editable, nor v.credentials, since a
  // credential-name mint could in principle change too) — an absent entry
  // reads the same as "idle". Cleared back to absent whenever the input it
  // tested (secret or broker) changes, so a stale "verified" never persists.
  const [testState, setTestState] = useState<Record<string, TestState>>({});

  const refresh = useCallback(() => {
    void commands.sendCommand("GetVenueSetup", {}).then((ack) => {
      if (ack.status === "accepted" && ack.value) {
        const s = ack.value as VenueSetup;
        setSetup(s);
        setDraft({ venues: s.file.venues.map((v) => ({ ...v })), gate: { global: { ...s.file.gate.global }, venue: { ...s.file.gate.venue } } });
        const keys = s.file.venues.map(() => crypto.randomUUID());
        setRowKeys(keys);
        setCapsByRow(Object.fromEntries(s.file.venues.map((v, i) => [keys[i], s.file.gate.venue[v.id] ?? zeroCaps()])));
      }
    }).catch(() => toast.push({ level: "danger", text: "Could not load venue setup." }));
  }, [commands, toast]);
  useEffect(refresh, [refresh]);

  const restartNeeded = useMemo(
    () => setup !== null && JSON.stringify(setup.file) !== JSON.stringify(setup.running),
    [setup],
  );

  // While a restart is in flight, engineState cycles open -> reconnecting ->
  // open as the engine drops and comes back (WsClient's own auto-reconnect;
  // no ws plumbing needed here). engineState is still "open" the instant
  // restarting flips true (the socket hasn't dropped yet — the ack arrives
  // before the engine's shutdown drain even starts), so this must wait for
  // an actual drop before treating a later "open" as "back". sawDropRef
  // tracks that: true only once engineState has been seen non-open during
  // this restart. On the "back to open" edge it refetches venue setup so
  // restartNeeded flips false and the banner clears itself once the new
  // engine reports running === file, instead of leaving a stale
  // "Restarting…" button forever.
  const sawDropRef = useRef(false);
  useEffect(() => {
    if (!restarting) {
      sawDropRef.current = false;
      return;
    }
    if (engineState !== "open") {
      sawDropRef.current = true;
      return;
    }
    if (sawDropRef.current) {
      setRestarting(false);
      setRestartConfirm(false);
      refresh();
    }
  }, [engineState, restarting, refresh]);

  // Client-side mirror of the engine's SetVenueSetup validation (settings
  // redesign design §6) — surfaced pre-save so users see errors before a
  // round-trip, not instead of the engine's own authoritative checks (its
  // ack.reason still renders in venues-error on rejection).
  const validation = useMemo<VenueIssues[]>(() => {
    const idCounts = new Map<string, number>();
    draft.venues.forEach((v) => idCounts.set(v.id, (idCounts.get(v.id) ?? 0) + 1));
    return draft.venues.map((v, i) => {
      const issues: VenueIssues = {};
      if (!v.id) issues.id = "id is required";
      else if (!VENUE_ID_RE.test(v.id)) issues.id = "id must be lowercase letters, digits, and -";
      else if ((idCounts.get(v.id) ?? 0) > 1) issues.id = "id must be unique";

      const typed = secretDrafts[v.credentials];
      const typedKeyId = !!typed?.keyId;
      const typedSecret = !!typed?.secret;
      const keySetForRow = v.credentials !== "" && (setup?.credKeys ?? []).includes(v.credentials);
      if (typedKeyId !== typedSecret) {
        issues.cred = "enter both key id and secret, or neither";
      } else if (CRED_REQUIRED_BROKERS.has(v.broker)) {
        // v.credentials !== "" guards against the sim empty-credentials
        // sentinel ever reaching here unminted — setBroker() is the primary
        // fix (mints a name the moment credentials become required), this
        // is defense in depth so an empty name can never look "satisfied".
        const satisfied = keySetForRow || (typedKeyId && typedSecret);
        if (!satisfied) issues.cred = `${BROKER_LABEL[v.broker] ?? v.broker} requires a credential key`;
      }

      // A testable broker (tradezero/alpaca) additionally requires a passing
      // Test connection before Save, UNLESS the venue is already fully
      // configured from a prior save (key on file, env+accountId present)
      // AND nothing about its credential was just retyped this session — a
      // pure risk-limit edit on an unchanged, already-verified venue must not
      // be forced through a fresh probe.
      if (TESTABLE_BROKERS.has(v.broker)) {
        const rowKey = rowKeys[i];
        const tested = testState[rowKey]?.status === "ok";
        const typedNewKeys = typedKeyId && typedSecret;
        const preexistingComplete = keySetForRow && !!v.env && (v.broker !== "tradezero" || !!v.accountId);
        const verified = tested || (preexistingComplete && !typedNewKeys);
        if (!verified) issues.cred = issues.cred ?? "test connection before saving";
      }

      if (v.broker === "tradezero" && !v.accountId) issues.account = "account id is required for TradeZero";
      return issues;
    });
  }, [draft.venues, secretDrafts, setup, rowKeys, testState]);
  const hasErrors = validation.some((i) => Object.keys(i).length > 0);

  const patchVenue = (i: number, over: Partial<Venue>) =>
    setDraft((d) => ({ ...d, venues: d.venues.map((v, j) => (j === i ? { ...v, ...over } : v)) }));
  const setEnv = (i: number, env: string) => patchVenue(i, { env });
  // A Test result is only meaningful for the exact broker/credential it was
  // run against — invalidate a row's result back to absent/idle whenever
  // either changes (called from setBroker and setSecretField below).
  const clearTestState = (rowKey: string | undefined) => {
    if (!rowKey) return;
    setTestState((s) => {
      if (!(rowKey in s)) return s;
      const next = { ...s };
      delete next[rowKey];
      return next;
    });
  };
  // Broker switch: a venue can arrive from the engine with credentials: ""
  // (the sim sentinel), then have its broker switched to one that needs a
  // credential. Mint a name right here — the same way addVenue() mints one
  // for brand-new rows — so credentials is never still "" once the
  // CREDENTIALS group appears. Never mint over an existing non-empty name
  // (that would orphan the credential it already points at).
  const setBroker = (i: number, broker: string) => {
    patchVenue(i, {
      broker,
      credentials: broker !== "sim" && !draft.venues[i].credentials ? mintCredName() : draft.venues[i].credentials,
    });
    // A Test result from the previous broker is meaningless once the broker
    // itself changes — invalidate it back to absent/idle.
    clearTestState(rowKeys[i]);
  };
  const addVenue = () => {
    const key = crypto.randomUUID();
    setRowKeys((k) => [...k, key]);
    setCapsByRow((c) => ({ ...c, [key]: zeroCaps() }));
    setDraft((d) => ({
      ...d,
      venues: [
        ...d.venues,
        { id: "", broker: "sim", env: "paper", credentials: mintCredName(), accountId: "", startingBalance: 100000, slippageBps: 0, fillLatencyMs: 0 },
      ],
    }));
  };
  const removeVenue = (i: number) => {
    const gone = draft.venues[i];
    const goneKey = rowKeys[i];
    setDraft((d) => ({ ...d, venues: d.venues.filter((_, j) => j !== i) }));
    setRowKeys((k) => k.filter((_, j) => j !== i));
    if (goneKey) setCapsByRow((c) => {
      if (!(goneKey in c)) return c;
      const next = { ...c };
      delete next[goneKey];
      return next;
    });
    if (gone) setSecretDrafts((d) => {
      if (!(gone.credentials in d)) return d;
      const next = { ...d };
      delete next[gone.credentials];
      return next;
    });
  };
  const setSecretField = (credName: string, field: "keyId" | "secret", value: string) => {
    setSecretDrafts((d) => ({ ...d, [credName]: { keyId: d[credName]?.keyId ?? "", secret: d[credName]?.secret ?? "", [field]: value } }));
    // A Test result was for the credential as it stood before this edit —
    // once the key id or secret changes, that result must not silently keep
    // reading as "verified".
    const idx = draft.venues.findIndex((v) => v.credentials === credName);
    if (idx >= 0) clearTestState(rowKeys[idx]);
  };

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

      // 2. Project capsByRow (the live editing source of truth, keyed by each
      //    row's stable synthetic key — never touched by id edits) into the
      //    wire's id-keyed Gate.venue shape, using each venue's *current* id.
      //    This guarantees every venue gets a gate entry — so the engine's
      //    fail-closed guard (no entry => block) never fires on a UI-created
      //    venue — and a rename always carries its row's caps to the new id,
      //    since the row/caps association was never id-based to begin with.
      const venueGate: Gate["venue"] = {};
      draft.venues.forEach((v, i) => {
        if (!v.id) return; // empty id is already blocked by validation
        venueGate[v.id] = capsByRow[rowKeys[i]] ?? zeroCaps();
      });
      const gate: Gate = { global: draft.gate.global, venue: venueGate };
      const setAck = await commands.sendCommand("SetVenueSetup", { venues: draft.venues, gate });
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

  // Live command, not a file edit — see the header comment's exception note.
  const resetBalance = async (v: Venue) => {
    try {
      const ack = await commands.sendCommand("ResetBalance", { venue: v.id });
      if (ack.status !== "accepted") {
        toast.push({ level: "danger", text: ack.reason || "rejected" });
        return;
      }
      toast.push({ level: "info", text: `${v.id} reset to $${v.startingBalance.toLocaleString()}` });
    } catch {
      toast.push({ level: "danger", text: "Reset failed (transport)." });
    }
  };

  // Live command (RestartEngine), not a file edit. The engine acks
  // "accepted" ~200ms before it actually cancels its context and starts
  // shutting down (see commands.go's restartAckFlushDelay), so this await
  // resolves cleanly before the socket ever drops -- no fire-and-forget
  // hack needed. Leaves `restarting` true on success; the reconnect effect
  // above clears it once the engine is back and running === file.
  const restartEngine = async () => {
    setRestarting(true);
    try {
      const ack = await commands.sendCommand("RestartEngine", {});
      if (ack.status !== "accepted") {
        toast.push({ level: "danger", text: ack.reason || "Restart rejected" });
        setRestarting(false);
        setRestartConfirm(false);
      }
    } catch {
      toast.push({ level: "danger", text: "Restart failed (transport)." });
      setRestarting(false);
      setRestartConfirm(false);
    }
  };

  // Read-only probe against the real broker (settings header comment's
  // FILE-ONLY rule still holds — this never places an order and never writes
  // config; a successful result only patches the in-memory draft's env/
  // accountId, which still requires Save to persist).
  const testConnection = async (i: number) => {
    const v = draft.venues[i];
    const rowKey = rowKeys[i];
    setTestState((s) => ({ ...s, [rowKey]: { status: "testing" } }));
    const typed = secretDrafts[v.credentials] ?? { keyId: "", secret: "" };
    try {
      const ack = await commands.sendCommand("TestConnection", {
        broker: v.broker, env: v.env, credentials: v.credentials,
        keyId: typed.keyId, secretKey: typed.secret, accountId: v.accountId,
      });
      const r = ack.value as TestConnectionResult | undefined;
      if (ack.status === "accepted" && r?.ok) {
        patchVenue(i, { env: r.env || v.env, accountId: r.accountId || v.accountId });
        const message = `Connected · ${(r.env || v.env).toUpperCase()}${r.accountId ? " · " + r.accountId : ""}`;
        setTestState((s) => ({ ...s, [rowKey]: { status: "ok", message, accounts: r.accounts } }));
        toast.push({ level: "success", text: message });
      } else {
        const message = r?.message || ack.reason || "Test failed";
        setTestState((s) => ({ ...s, [rowKey]: { status: "fail", message } }));
        toast.push({ level: "danger", text: message });
      }
    } catch {
      setTestState((s) => ({ ...s, [rowKey]: { status: "fail", message: "Test failed (transport)." } }));
      toast.push({ level: "danger", text: "Test failed (transport)." });
    }
  };

  const field = { className: "field" } as const; // spread onto native inputs/selects for shared look
  const groupLabel = { marginBottom: 4 } as const;
  const fieldWrap = { display: "flex", flexDirection: "column", gap: 2, fontSize: 10.5, color: palette.textMuted } as const;
  const issueText = { color: palette.danger, fontSize: 10, marginTop: 2 } as const;

  return (
    <div style={{ color: palette.text }}>
      {restartNeeded && (
        <div data-testid="restart-banner" style={{ background: palette.bg, border: `1px solid ${palette.accent}`, color: palette.accent, padding: "8px 12px", borderRadius: 4, marginBottom: 12, fontSize: 12, display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
          <span>⚠ Engine restart required — saved venue config differs from the running engine.</span>
          <span style={{ display: "flex", alignItems: "center", gap: 6, flexShrink: 0 }}>
            {restartConfirm && !restarting && (
              <span style={{ fontSize: 11 }}>Restart now? This briefly interrupts live data.</span>
            )}
            <button
              data-testid="restart-engine"
              className="btn"
              disabled={restarting}
              onClick={() => (restartConfirm ? void restartEngine() : setRestartConfirm(true))}
            >
              {restarting ? "Restarting…" : restartConfirm ? "Confirm restart" : "Restart now"}
            </button>
            {restartConfirm && !restarting && (
              <button className="btn" onClick={() => setRestartConfirm(false)}>Cancel</button>
            )}
          </span>
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
        const showStartingBalance = v.broker === "sim";
        const canReset = v.broker === "sim" && (setup?.running.venues ?? []).some((rv) => rv.id === v.id && rv.broker === "sim");
        const keySet = !!(setup?.credKeys ?? []).includes(v.credentials);
        const issue = validation[i];
        const typed = secretDrafts[v.credentials] ?? { keyId: "", secret: "" };
        const removing = removeConfirmIdx === i;
        const resetting = resetConfirmIdx === i;
        const testable = TESTABLE_BROKERS.has(v.broker);
        const test = testState[rowKeys[i]];
        const showManualEnvAccount = !testable; // tradezero/alpaca auto-detect instead

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
              {canReset && (
                <>
                  {resetting && (
                    <span style={{ fontSize: 11, color: palette.accent, marginRight: 4 }}>
                      Reset to ${v.startingBalance.toLocaleString()}?
                    </span>
                  )}
                  <button
                    data-testid={`venue-reset-${i}`}
                    className="btn"
                    onClick={() => (resetting ? (void resetBalance(v), setResetConfirmIdx(null)) : setResetConfirmIdx(i))}
                  >
                    {resetting ? "Confirm reset" : "Reset balance"}
                  </button>
                  {resetting && <button className="btn" onClick={() => setResetConfirmIdx(null)}>Cancel</button>}
                </>
              )}
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
                      onChange={(e) => setBroker(i, e.target.value)} style={{ width: 100 }}>
                      {BROKERS.map((b) => <option key={b} value={b}>{BROKER_LABEL[b]}</option>)}
                    </select>
                  </label>
                  {showManualEnvAccount ? (
                    <label style={fieldWrap}>
                      env
                      <select {...field} data-testid={`venue-env-${i}`} value={v.env}
                        onChange={(e) => setEnv(i, e.target.value)} style={{ width: 80 }}>
                        {ENVS.map((x) => <option key={x} value={x}>{x}</option>)}
                      </select>
                    </label>
                  ) : (
                    <label style={fieldWrap}>
                      env
                      <span className={`chip ${isLive ? "chip-live" : ""}`} data-testid={`venue-env-detected-${i}`}
                        style={!isLive ? { color: palette.textMuted } : undefined}>
                        {v.env ? v.env.toUpperCase() : "—"}
                      </span>
                    </label>
                  )}
                  {showManualEnvAccount && (
                    <label style={fieldWrap}>
                      account id
                      <input {...field} data-testid={`venue-account-${i}`} value={v.accountId}
                        onChange={(e) => patchVenue(i, { accountId: e.target.value })} placeholder="account id" style={{ width: 110 }} />
                    </label>
                  )}
                  {v.broker === "tradezero" && (
                    test?.accounts && test.accounts.length > 1 ? (
                      <label style={fieldWrap}>
                        account id
                        <select {...field} data-testid={`venue-account-select-${i}`} value={v.accountId}
                          onChange={(e) => patchVenue(i, { accountId: e.target.value })} style={{ width: 160 }}>
                          <option value="">select account</option>
                          {test.accounts.map((a) => (
                            <option key={a.accountId} value={a.accountId}>{a.accountId} · {a.accountType}</option>
                          ))}
                        </select>
                      </label>
                    ) : (
                      <label style={fieldWrap}>
                        account id
                        <span className="mono" data-testid={`venue-account-detected-${i}`} style={{ padding: "2px 0" }}>
                          {v.accountId || "—"}
                        </span>
                      </label>
                    )
                  )}
                  {showStartingBalance && (
                    <label style={fieldWrap}>
                      starting balance
                      <input {...field} className="field mono" type="number" data-testid={`venue-startingbalance-${i}`}
                        value={String(v.startingBalance ?? 0)}
                        onChange={(e) => patchVenue(i, { startingBalance: Number(e.target.value) || 0 })}
                        style={{ width: 100 }} />
                    </label>
                  )}
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
                    {testable && (
                      <button data-testid={`venue-test-${i}`} className="btn"
                        disabled={test?.status === "testing"} onClick={() => void testConnection(i)}>
                        {test?.status === "testing" ? "Testing…" : "Test connection"}
                      </button>
                    )}
                  </div>
                  <div style={{ color: palette.textMuted, fontSize: 10, marginTop: 4 }}>leave blank to keep the existing key</div>
                  {testable && test?.message && (
                    <div data-testid={`venue-test-result-${i}`}
                      style={{ ...issueText, color: test.status === "ok" ? palette.ok : palette.danger }}>
                      {test.status === "ok" ? "✓ " : "✗ "}{test.message}
                    </div>
                  )}
                  {issue.cred && <div style={issueText}>{issue.cred}</div>}
                </div>
              )}

              <div>
                <div className="col-head" style={groupLabel}>Risk limits</div>
                <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "flex-end" }}>
                  {GATE_CAPS.map((cap) => (
                    <label key={cap} style={fieldWrap}>
                      {cap}
                      <input {...field} className="field mono" value={String(capsByRow[rowKeys[i]]?.[cap] ?? 0)}
                        onChange={(e) => {
                          const key = rowKeys[i];
                          setCapsByRow((c) => ({ ...c, [key]: { ...(c[key] ?? zeroCaps()), [cap]: Number(e.target.value) || 0 } }));
                        }}
                        style={{ width: 72 }} />
                    </label>
                  ))}
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
