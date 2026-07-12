// Venues & credentials editor. Edits are FILE-ONLY: SetVenueSetup rewrites
// config.toml and PutCredential/DeleteCredential rewrite credentials.json;
// nothing here arms a venue or changes the running gate — changes apply at the
// next engine restart (hence the restart banner). ONE deliberate exception:
// "Reset balance" on the sim venue sends a live ResetBalance command straight
// to the running exec.Core (the same channel Flatten/Arm elsewhere use) rather
// than writing config — funding/resetting a sim account is inherently a live
// action, not a file edit, and the button only ever appears for a venue
// that's already running as broker "sim".
//
// Fixed-roster card model (venues broker-cards design rev 2, §C): four cards
// in fixed order — Simulator, moomoo, Alpaca (paper+live slots), TradeZero —
// each a PROJECTION of draft.venues, claiming the first venue matching its
// (broker, env) predicate (see resolveSlots). Legacy/nonstandard ids are
// claimed as-is and never renamed; venues beyond the roster render read-only
// in "Other venues" and are never mutated, so they round-trip through Save
// byte-for-byte. New venues from a card action get canonical ids (moomoo,
// alpaca, alpaca-live, tradezero, sim) — there is no more user-typed venue-id
// field anywhere in this form.
//
// Credentials model: ONE opaque key per venue (no more shared/named credential
// picker). Configured rows mint a stable, user-invisible `key-<uuid8>` name
// into venue.credentials (never re-minted once set). Unconfigured Alpaca/
// TradeZero slots keep typed key/secret in secretDrafts under a fixed
// PENDING_* key until Save, which mints the credential name and materializes
// the venue only then ("Filling a slot creates its venue on Save" — moomoo is
// the one exception: its Enable button adds the venue to the draft
// immediately, still requiring the normal Save to persist). Typed
// key/secret are write-only, never seeded from `setup` — the engine never
// sends secrets back — and cleared after every Save.
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { useTheme } from "../ThemeProvider";
import type { Palette } from "../../render/palette";
import { useToasts } from "../Toast";
import { Button } from "../controls/Button";
import type { HealthStore } from "../../data/HealthStore";
import type { ExecStore } from "../../data/ExecStore";
import type { AckMsg, Venue, Gate, GateLimitsView, VenueConfig, VenueSetup, TestConnectionResult, TestAccount } from "../../wire/contract";
import type { ConnState } from "../../wire/WsClient";

interface Commands { sendCommand(name: string, args: unknown): Promise<AckMsg>; }

const BROKER_LABEL: Record<string, string> = { tradezero: "TradeZero", alpaca: "Alpaca", moomoo: "moomoo", sim: "Simulated" };
const GATE_CAPS = ["maxOrderValue", "maxPositionValue", "maxPositionShares", "maxOpenOrders"] as const;
const GLOBAL_CAPS = ["maxDayLoss", "maxSymbolPositionValue", "maxSymbolPositionShares"] as const;

// Fixed keys for a roster slot with no venue yet — stable across renders
// (unlike rowKeys, which are per-mount crypto.randomUUID()s), since there is
// at most one such virtual row per slot. Once Save materializes the venue,
// refresh() regenerates rowKeys and the card switches to reading the real
// row; secretDrafts/testState under these keys are cleared on every Save so
// a later Remove (un-configuring the slot again) never resurrects stale
// "verified" state under the same fixed key.
const PENDING_ALPACA_PAPER = "__pending_alpaca_paper__";
const PENDING_ALPACA_LIVE = "__pending_alpaca_live__";
const PENDING_TRADEZERO = "__pending_tradezero__";

const emptyGate = (): Gate => ({ global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venue: {} });
const mintCredName = () => `key-${crypto.randomUUID().slice(0, 8)}`;
const zeroCaps = (): GateLimitsView => ({ maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 });

interface SecretDraft { keyId: string; secret: string }
type TestStatus = "idle" | "testing" | "ok" | "fail";
interface TestState { status: TestStatus; message?: string; accounts?: TestAccount[]; env?: string; accountId?: string }

interface Slots { sim: number; moomoo: number; alpacaPaper: number; alpacaLive: number; tradezero: number }

// Each slot claims the FIRST venue matching its (broker, env) predicate.
// Broker values are mutually exclusive per venue, so independent `if`s (not
// an if/else chain) are just as correct — kept as ifs for clarity, not for
// any exclusivity reason.
function resolveSlots(venues: Venue[]): Slots {
  const s: Slots = { sim: -1, moomoo: -1, alpacaPaper: -1, alpacaLive: -1, tradezero: -1 };
  venues.forEach((v, i) => {
    if (v.broker === "sim" && s.sim === -1) s.sim = i;
    if (v.broker === "moomoo" && s.moomoo === -1) s.moomoo = i;
    if (v.broker === "alpaca" && v.env === "live" && s.alpacaLive === -1) s.alpacaLive = i;
    if (v.broker === "alpaca" && v.env !== "live" && s.alpacaPaper === -1) s.alpacaPaper = i;
    if (v.broker === "tradezero" && s.tradezero === -1) s.tradezero = i;
  });
  return s;
}

export function VenuesSection({ commands, engineState, health, exec }: {
  commands: Commands;
  engineState?: ConnState | undefined;
  // Live OpenD-reachability (sys.health "engine-moomoo" link) and per-venue
  // connected/note status (exec.status) for the moomoo card. Both are the
  // app's single existing source of these facts (subscribed once in
  // App.tsx into stores.health/stores.exec) — passed down as values rather
  // than re-subscribed here, so this form is never a second live source of
  // either fact. Optional so tests that construct this component directly
  // (no stores wiring) keep compiling; absent means the moomoo card renders
  // its pre-venue "waiting" copy and skips the connection chip.
  health?: HealthStore | undefined;
  exec?: ExecStore | undefined;
}): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const [setup, setSetup] = useState<VenueSetup | null>(null);
  const [draft, setDraft] = useState<VenueConfig>({ venues: [], gate: emptyGate() });
  const [err, setErr] = useState("");
  const [dirty, setDirty] = useState(false);
  const [staleDraft, setStaleDraft] = useState(false);
  // Write-only typed secrets, keyed by the venue's opaque `credentials` name
  // for a configured row, or by a fixed PENDING_* key for an unconfigured
  // Alpaca/TradeZero slot — never populated from a refresh.
  const [secretDrafts, setSecretDrafts] = useState<Record<string, SecretDraft>>({});
  const [restarting, setRestarting] = useState(false);
  // Stable per-row identity for risk-limit caps, independent of the venue's
  // `id` — see capsByRow's own note below.
  const [rowKeys, setRowKeys] = useState<string[]>([]);
  const [capsByRow, setCapsByRow] = useState<Record<string, GateLimitsView>>({});
  const [limitsOpen, setLimitsOpen] = useState<Record<string, boolean>>({});
  // Test-connection outcome per row, keyed by rowKeys[idx] (configured) or a
  // PENDING_* key (unconfigured slot). Absent reads as "idle".
  const [testState, setTestState] = useState<Record<string, TestState>>({});
  // moomoo's own probe result, session-local (never persisted) — null until
  // "Check OpenD" is clicked, or after Enable materializes the venue.
  const [moomooProbe, setMoomooProbe] = useState<TestConnectionResult | null>(null);
  const [moomooProbing, setMoomooProbing] = useState(false);
  const [moomooSelectedAccount, setMoomooSelectedAccount] = useState("");

  const refresh = useCallback(() => {
    void commands.sendCommand("GetVenueSetup", {}).then((ack) => {
      if (ack.status === "accepted" && ack.value) {
        const s = ack.value as VenueSetup;
        setSetup(s);
        const venues = s.file.venues.map((v) => ({ ...v }));
        // Mirror the write path's env guarantees ("sim is always paper",
        // "moomoo is always live") on load, scoped to ONLY the roster-claimed
        // venue for each — an older build's manual dropdown (or a hand-edited
        // config.toml) could still have these on disk. Overflow venues of the
        // same broker (a second sim/moomoo row) are deliberately left
        // untouched: the legacy-overflow round-trip invariant means this form
        // must never rewrite a venue it has no card for.
        const sl = resolveSlots(venues);
        if (sl.sim !== -1) venues[sl.sim] = { ...venues[sl.sim], env: "paper" };
        if (sl.moomoo !== -1) venues[sl.moomoo] = { ...venues[sl.moomoo], env: "live" };
        setDraft({ venues, gate: { global: { ...s.file.gate.global }, venue: { ...s.file.gate.venue } } });
        const keys = venues.map(() => crypto.randomUUID());
        setRowKeys(keys);
        setCapsByRow(Object.fromEntries(venues.map((v, i) => [keys[i], s.file.gate.venue[v.id] ?? zeroCaps()])));
        setDirty(false);
        setStaleDraft(false);
        setMoomooProbe(null);
      }
    }).catch(() => toast.push({ level: "danger", text: "Could not load venue setup." }));
  }, [commands, toast]);
  useEffect(refresh, [refresh]);

  const slots = useMemo(() => resolveSlots(draft.venues), [draft.venues]);
  const claimed = useMemo(
    () => new Set([slots.sim, slots.moomoo, slots.alpacaPaper, slots.alpacaLive, slots.tradezero].filter((i) => i !== -1)),
    [slots],
  );
  const overflow = useMemo(() => draft.venues.map((_, i) => i).filter((i) => !claimed.has(i)), [draft.venues, claimed]);

  const restartNeeded = useMemo(
    () => setup !== null && JSON.stringify(setup.file) !== JSON.stringify(setup.running),
    [setup],
  );

  // Same reconnect-then-reload contract as before restart: engineState cycles
  // open -> reconnecting -> open around a real engine restart; only reload on
  // the edge back to "open" after an actual drop was observed, never on the
  // ack's own instant (still "open", socket hasn't dropped yet).
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
      window.location.reload();
    }
  }, [engineState, restarting]);

  // Subscribe (not just read) health/exec — a plain getSnapshot() call would
  // never re-render this component when the store's data changes, since
  // React has no way to know the value became stale. Mirrors AppShell's own
  // `useSyncExternalStore(...); const x = store.accessor();` pattern.
  useSyncExternalStore((cb) => (health ? health.subscribe(cb) : () => {}), () => health?.getSnapshot());
  useSyncExternalStore((cb) => (exec ? exec.subscribe(cb) : () => {}), () => exec?.getSnapshot());
  const healthState = health?.getSnapshot();
  const execStatus = exec?.status() ?? null;

  // Stale-draft reload guard (§A "Stale-draft race"): a boot-time seed write
  // or a manual moomoo Enable+Save on another client broadcasts a
  // venue.seeded/venue.seed_declined sys.event; react only to events newer
  // than the baseline captured on mount (the health store's `events` already
  // includes replayed history, which must never re-trigger this).
  const seenSeqRef = useRef<number | null>(null);
  useEffect(() => {
    const events = healthState?.events ?? [];
    const relevant = events.filter((e) => e.kind === "venue.seeded" || e.kind === "venue.seed_declined");
    // -1 when there are no relevant events yet — a legitimate baseline (NOT
    // an "empty, skip" signal), so the very first real event that ever
    // arrives after mount still compares as newer and reacts.
    const maxSeq = relevant.reduce((m, e) => Math.max(m, e.seq), -1);
    if (seenSeqRef.current === null) { seenSeqRef.current = maxSeq; return; } // baseline only, no reaction
    if (maxSeq <= seenSeqRef.current) return;
    seenSeqRef.current = maxSeq;
    if (dirty) setStaleDraft(true);
    else refresh();
  }, [healthState?.events]);

  const engineMoomooLink = healthState?.links.find((l) => l.link === "engine-moomoo");
  const moomooLinkUp = engineMoomooLink != null && engineMoomooLink.status !== "down";

  const patchVenue = (i: number, over: Partial<Venue>) => {
    setDraft((d) => ({ ...d, venues: d.venues.map((v, j) => (j === i ? { ...v, ...over } : v)) }));
    setDirty(true);
  };
  const clearTestState = (key: string | undefined) => {
    if (!key) return;
    setTestState((s) => {
      if (!(key in s)) return s;
      const next = { ...s };
      delete next[key];
      return next;
    });
  };
  const setSecretField = (credName: string, field: "keyId" | "secret", value: string) => {
    setSecretDrafts((d) => ({ ...d, [credName]: { keyId: d[credName]?.keyId ?? "", secret: d[credName]?.secret ?? "", [field]: value } }));
    setDirty(true);
    const idx = draft.venues.findIndex((v) => v.credentials === credName);
    clearTestState(idx >= 0 ? rowKeys[idx] : credName);
  };
  const updateCap = (rowKey: string, cap: (typeof GATE_CAPS)[number], value: number) => {
    setCapsByRow((c) => ({ ...c, [rowKey]: { ...(c[rowKey] ?? zeroCaps()), [cap]: value } }));
    setDirty(true);
  };

  const addSimulator = () => {
    const key = crypto.randomUUID();
    setRowKeys((k) => [...k, key]);
    setCapsByRow((c) => ({ ...c, [key]: zeroCaps() }));
    setDraft((d) => ({
      ...d,
      venues: [...d.venues, { id: "sim", broker: "sim", env: "paper", credentials: "", accountId: "", startingBalance: 100000, slippageBps: 0, fillLatencyMs: 0 }],
    }));
    setDirty(true);
  };

  const removeVenue = (i: number) => {
    const gone = draft.venues[i];
    const goneKey = rowKeys[i];
    setDraft((d) => ({ ...d, venues: d.venues.filter((_, j) => j !== i) }));
    setRowKeys((k) => k.filter((_, j) => j !== i));
    setDirty(true);
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

  const restartEngine = async () => {
    setRestarting(true);
    try {
      const ack = await commands.sendCommand("RestartEngine", {});
      if (ack.status !== "accepted") {
        toast.push({ level: "danger", text: ack.reason || "Restart rejected" });
        setRestarting(false);
      }
    } catch {
      toast.push({ level: "danger", text: "Restart failed (transport)." });
      setRestarting(false);
    }
  };

  // Read-only probe for an EXISTING configured row (Alpaca/TradeZero) — a
  // successful result only patches the in-memory draft's env/accountId,
  // which still requires Save to persist.
  const testExisting = async (idx: number) => {
    const v = draft.venues[idx];
    const rowKey = rowKeys[idx];
    setTestState((s) => ({ ...s, [rowKey]: { status: "testing" } }));
    const typed = secretDrafts[v.credentials] ?? { keyId: "", secret: "" };
    try {
      const ack = await commands.sendCommand("TestConnection", {
        broker: v.broker, env: v.env, credentials: v.credentials,
        keyId: typed.keyId, secretKey: typed.secret, accountId: v.accountId,
      });
      const r = ack.value as TestConnectionResult | undefined;
      if (ack.status === "accepted" && r?.ok) {
        if (v.broker === "alpaca" && r.env && r.env !== v.env) {
          const message = v.env === "paper"
            ? "This key belongs to a live account — paste it into the Live slot below."
            : "This key belongs to a paper account — paste it into the Paper slot above.";
          setTestState((s) => ({ ...s, [rowKey]: { status: "fail", message } }));
          toast.push({ level: "danger", text: message });
          return;
        }
        patchVenue(idx, { env: r.env || v.env, accountId: r.accountId || v.accountId });
        const message = `Connected · ${(r.env || v.env).toUpperCase()}${r.accountId ? " · " + r.accountId : ""}`;
        setTestState((s) => ({ ...s, [rowKey]: { status: "ok", message, accounts: r.accounts, env: r.env || v.env, accountId: r.accountId || v.accountId } }));
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

  // Same probe, for an UNCONFIGURED Alpaca/TradeZero slot — nothing exists in
  // draft.venues yet, so the result is stashed in testState under the fixed
  // pending key; saveVenues() reads it back to materialize the venue.
  const testPending = async (pendingKey: string, broker: string, env: string, enforceEnv: boolean) => {
    setTestState((s) => ({ ...s, [pendingKey]: { status: "testing" } }));
    const typed = secretDrafts[pendingKey] ?? { keyId: "", secret: "" };
    try {
      const ack = await commands.sendCommand("TestConnection", {
        broker, env, credentials: "", keyId: typed.keyId, secretKey: typed.secret, accountId: "",
      });
      const r = ack.value as TestConnectionResult | undefined;
      if (ack.status === "accepted" && r?.ok) {
        if (enforceEnv && r.env && r.env !== env) {
          const message = env === "paper"
            ? "This key belongs to a live account — paste it into the Live slot below."
            : "This key belongs to a paper account — paste it into the Paper slot above.";
          setTestState((s) => ({ ...s, [pendingKey]: { status: "fail", message } }));
          toast.push({ level: "danger", text: message });
          return;
        }
        const detectedEnv = r.env || env;
        const message = `Connected · ${detectedEnv.toUpperCase()}${r.accountId ? " · " + r.accountId : ""}`;
        setTestState((s) => ({ ...s, [pendingKey]: { status: "ok", message, accounts: r.accounts, env: detectedEnv, accountId: r.accountId } }));
        toast.push({ level: "success", text: message });
      } else {
        const message = r?.message || ack.reason || "Test failed";
        setTestState((s) => ({ ...s, [pendingKey]: { status: "fail", message } }));
        toast.push({ level: "danger", text: message });
      }
    } catch {
      setTestState((s) => ({ ...s, [pendingKey]: { status: "fail", message: "Test failed (transport)." } }));
      toast.push({ level: "danger", text: "Test failed (transport)." });
    }
  };

  const probeMoomoo = async () => {
    setMoomooProbing(true);
    try {
      const ack = await commands.sendCommand("TestConnection", {
        broker: "moomoo", env: "live", credentials: "", keyId: "", secretKey: "", accountId: "",
      });
      const r = ack.value as TestConnectionResult | undefined;
      if (ack.status === "accepted" && r) {
        setMoomooProbe(r);
        setMoomooSelectedAccount(r.accountId || r.accounts[0]?.accountId || "");
      } else {
        setMoomooProbe({ ok: false, env: "", accountId: "", accountType: "", message: ack.reason || "Check failed", accounts: [] });
      }
    } catch {
      setMoomooProbe({ ok: false, env: "", accountId: "", accountType: "", message: "Check failed (transport).", accounts: [] });
    } finally {
      setMoomooProbing(false);
    }
  };

  const enableMoomoo = () => {
    if (!moomooSelectedAccount) return;
    const key = crypto.randomUUID();
    setRowKeys((k) => [...k, key]);
    setCapsByRow((c) => ({ ...c, [key]: zeroCaps() }));
    setDraft((d) => ({
      ...d,
      venues: [...d.venues, { id: "moomoo", broker: "moomoo", env: "live", credentials: "", accountId: moomooSelectedAccount, startingBalance: 0, slippageBps: 0, fillLatencyMs: 0 }],
    }));
    setDirty(true);
    setMoomooProbe(null);
  };

  // Per-slot Save validation. Unlike the old per-row model there is no id
  // field to validate here at all (ids are canonical or preserved-legacy,
  // never user-typed); the engine's own SetVenueSetup rejection is still the
  // authoritative check (surfaced via `err` on failure) for anything this
  // client-side pass doesn't cover, e.g. an id collision with an overflow venue.
  const slotIssues = useMemo(() => {
    const issues: { alpacaPaper?: string; alpacaLive?: string; tradezero?: string } = {};

    const checkExisting = (idx: number, key: "alpacaPaper" | "alpacaLive" | "tradezero", broker: string) => {
      const v = draft.venues[idx];
      const typed = secretDrafts[v.credentials];
      const typedKeyId = !!typed?.keyId, typedSecret = !!typed?.secret;
      if (typedKeyId !== typedSecret) { issues[key] = "enter both key id and secret, or neither"; return; }
      const tested = testState[rowKeys[idx]]?.status === "ok";
      const preexistingComplete = !!v.env && (broker !== "tradezero" || !!v.accountId);
      const verified = tested || (preexistingComplete && !(typedKeyId && typedSecret));
      if (!verified) { issues[key] = "test connection before saving"; return; }
      if (broker === "tradezero" && !v.accountId) issues[key] = "account id is required for TradeZero";
    };
    const checkPending = (pendingKey: string, key: "alpacaPaper" | "alpacaLive" | "tradezero", broker: string) => {
      const typed = secretDrafts[pendingKey];
      const typedKeyId = !!typed?.keyId, typedSecret = !!typed?.secret;
      if (!typedKeyId && !typedSecret) return; // untouched slot: nothing to validate, nothing to save
      if (typedKeyId !== typedSecret) { issues[key] = "enter both key id and secret, or neither"; return; }
      const ts = testState[pendingKey];
      if (ts?.status !== "ok") { issues[key] = "test connection before saving"; return; }
      if (broker === "tradezero" && !ts.accountId) issues[key] = "account id is required for TradeZero";
    };

    if (slots.alpacaPaper !== -1) checkExisting(slots.alpacaPaper, "alpacaPaper", "alpaca"); else checkPending(PENDING_ALPACA_PAPER, "alpacaPaper", "alpaca");
    if (slots.alpacaLive !== -1) checkExisting(slots.alpacaLive, "alpacaLive", "alpaca"); else checkPending(PENDING_ALPACA_LIVE, "alpacaLive", "alpaca");
    if (slots.tradezero !== -1) checkExisting(slots.tradezero, "tradezero", "tradezero"); else checkPending(PENDING_TRADEZERO, "tradezero", "tradezero");
    return issues;
  }, [draft.venues, secretDrafts, rowKeys, testState, slots]);
  const hasErrors = Object.values(slotIssues).some(Boolean);

  // Materializes any unconfigured Alpaca/TradeZero slot that has a typed,
  // tested key into a real venue with a canonical id — deferred to Save time
  // per the design ("filling a slot creates its venue on Save"), unlike
  // moomoo's Enable button, which writes into the draft immediately.
  const buildFinalVenues = (): { venues: Venue[]; newCreds: Array<{ credName: string; keyId: string; secret: string }> } => {
    const venues = draft.venues.map((v) => ({ ...v }));
    const newCreds: Array<{ credName: string; keyId: string; secret: string }> = [];
    const materialize = (pendingKey: string, id: string, broker: string, fallbackEnv: string) => {
      const typed = secretDrafts[pendingKey];
      if (!typed?.keyId || !typed?.secret) return;
      const detected = testState[pendingKey];
      const credName = mintCredName();
      venues.push({
        id, broker, env: detected?.env || fallbackEnv, credentials: credName,
        accountId: detected?.accountId || "", startingBalance: 0, slippageBps: 0, fillLatencyMs: 0,
      });
      newCreds.push({ credName, keyId: typed.keyId, secret: typed.secret });
    };
    if (slots.alpacaPaper === -1) materialize(PENDING_ALPACA_PAPER, "alpaca", "alpaca", "paper");
    if (slots.alpacaLive === -1) materialize(PENDING_ALPACA_LIVE, "alpaca-live", "alpaca", "live");
    if (slots.tradezero === -1) materialize(PENDING_TRADEZERO, "tradezero", "tradezero", "live");
    return { venues, newCreds };
  };

  const saveVenues = async () => {
    if (hasErrors) return; // Save is disabled in this state, but guard anyway
    setErr("");
    try {
      const { venues: finalVenues, newCreds } = buildFinalVenues();

      // 1. Push any newly-typed credentials first (existing rows, then newly
      //    materialized slots) — SetVenueSetup requires tradezero/alpaca
      //    venues to name an EXISTING key.
      for (const v of draft.venues) {
        const typed = secretDrafts[v.credentials];
        if (!typed?.keyId || !typed?.secret) continue;
        const ack = await commands.sendCommand("PutCredential", { name: v.credentials, keyId: typed.keyId, secretKey: typed.secret });
        if (ack.status !== "accepted") { setErr(ack.reason || "rejected"); return; }
      }
      for (const nc of newCreds) {
        const ack = await commands.sendCommand("PutCredential", { name: nc.credName, keyId: nc.keyId, secretKey: nc.secret });
        if (ack.status !== "accepted") { setErr(ack.reason || "rejected"); return; }
      }

      // 2. Project capsByRow into the wire's id-keyed Gate.venue shape for
      //    existing rows (by their CURRENT id — capsByRow is never id-keyed,
      //    so a rename never loses or swaps caps), plus an all-zero entry for
      //    every freshly materialized venue (moomoo Enable or an Alpaca/
      //    TradeZero slot just filled) — the engine's fail-closed gate guard
      //    (no entry => block) must never fire on a UI-created venue.
      const venueGate: Gate["venue"] = {};
      draft.venues.forEach((v, i) => { if (v.id) venueGate[v.id] = capsByRow[rowKeys[i]] ?? zeroCaps(); });
      for (const v of finalVenues) if (!(v.id in venueGate)) venueGate[v.id] = zeroCaps();
      const gate: Gate = { global: draft.gate.global, venue: venueGate };

      const setAck = await commands.sendCommand("SetVenueSetup", { venues: finalVenues, gate });
      if (setAck.status !== "accepted") { setErr(setAck.reason || "rejected"); return; }

      // 3. Best-effort cleanup: credential names no longer referenced by the
      //    saved venues (the venue was removed, not renamed) can be deleted.
      const kept = new Set(finalVenues.map((v) => v.credentials).filter(Boolean));
      const oldNames = (setup?.file.venues ?? []).map((v) => v.credentials).filter(Boolean);
      for (const name of oldNames) {
        if (kept.has(name)) continue;
        try { await commands.sendCommand("DeleteCredential", { name }); } catch { /* best-effort */ }
      }

      // 4. Clear write-only inputs/pending test state and reload.
      setSecretDrafts({});
      setTestState({});
      refresh();
    } catch {
      toast.push({ level: "danger", text: "Save failed (transport)." });
    }
  };

  const groupLabel = { marginBottom: 4 } as const;
  const fieldWrap = { display: "flex", flexDirection: "column", gap: 2, fontSize: 10.5, color: palette.textMuted } as const;

  return (
    <div style={{ color: palette.text }}>
      {restartNeeded && (
        <div data-testid="restart-banner" style={{ background: palette.bg, border: `1px solid ${palette.accent}`, color: palette.accent, padding: "8px 12px", borderRadius: 4, marginBottom: 12, fontSize: 12, display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
          <span>⚠ Engine restart required — saved venue config differs from the running engine.</span>
          <span style={{ display: "flex", alignItems: "center", gap: 6, flexShrink: 0 }}>
            <Button
              variant="danger" confirm confirmLabel="Confirm restart"
              data-testid="restart-engine" loading={restarting}
              onClick={() => void restartEngine()}
            >
              {restarting ? "Restarting…" : "Restart now"}
            </Button>
          </span>
        </div>
      )}
      {staleDraft && (
        <div data-testid="stale-draft-banner" style={{ background: palette.bg, border: `1px solid ${palette.danger}`, color: palette.danger, padding: "8px 12px", borderRadius: 4, marginBottom: 12, fontSize: 12, display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
          <span>Venue config changed on disk — your unsaved edits may be out of date.</span>
          <Button data-testid="stale-draft-reload" onClick={refresh}>Reload</Button>
        </div>
      )}
      {err && <div data-testid="venues-error" style={{ color: palette.danger, marginBottom: 8, fontSize: 12 }}>{err}</div>}

      <div className="serif" style={{ fontSize: 14, fontWeight: 600, marginBottom: 8 }}>Venues</div>

      <SimulatorCard
        palette={palette} venue={slots.sim !== -1 ? draft.venues[slots.sim] : null}
        running={slots.sim !== -1 ? (setup?.running.venues ?? []).some((rv) => rv.id === draft.venues[slots.sim].id && rv.broker === "sim") : false}
        rowKey={slots.sim !== -1 ? rowKeys[slots.sim] : undefined}
        caps={slots.sim !== -1 ? (capsByRow[rowKeys[slots.sim]] ?? zeroCaps()) : zeroCaps()}
        limitsExpanded={slots.sim !== -1 ? (limitsOpen[rowKeys[slots.sim]] ?? false) : false}
        onToggleLimits={() => { const k = rowKeys[slots.sim]; setLimitsOpen((s) => ({ ...s, [k]: !(s[k] ?? false) })); }}
        onUpdateCap={(cap, value) => updateCap(rowKeys[slots.sim], cap, value)}
        onPatch={(over) => patchVenue(slots.sim, over)}
        onAdd={addSimulator}
        onReset={() => void resetBalance(draft.venues[slots.sim])}
        fieldWrap={fieldWrap} groupLabel={groupLabel}
      />

      <MoomooCard
        palette={palette}
        venue={slots.moomoo !== -1 ? draft.venues[slots.moomoo] : null}
        fileHasIt={(setup?.file.venues ?? []).some((v) => v.broker === "moomoo")}
        runningStatus={slots.moomoo !== -1 ? (execStatus?.venues.find((v) => v.venue === draft.venues[slots.moomoo].id && v.broker === "moomoo")) : undefined}
        attempted={setup?.seed.moomooAttempted ?? false}
        linkUp={moomooLinkUp}
        probe={moomooProbe} probing={moomooProbing}
        selectedAccount={moomooSelectedAccount} onSelectAccount={setMoomooSelectedAccount}
        onProbe={() => void probeMoomoo()} onEnable={enableMoomoo}
        onRemove={slots.moomoo !== -1 ? () => removeVenue(slots.moomoo) : undefined}
        rowKey={slots.moomoo !== -1 ? rowKeys[slots.moomoo] : undefined}
        caps={slots.moomoo !== -1 ? (capsByRow[rowKeys[slots.moomoo]] ?? zeroCaps()) : zeroCaps()}
        limitsExpanded={slots.moomoo !== -1 ? (limitsOpen[rowKeys[slots.moomoo]] ?? false) : false}
        onToggleLimits={() => { const k = rowKeys[slots.moomoo]; setLimitsOpen((s) => ({ ...s, [k]: !(s[k] ?? false) })); }}
        onUpdateCap={(cap, value) => updateCap(rowKeys[slots.moomoo], cap, value)}
        fieldWrap={fieldWrap} groupLabel={groupLabel}
      />

      <AlpacaCard
        paper={{
          venue: slots.alpacaPaper !== -1 ? draft.venues[slots.alpacaPaper] : null,
          rowKey: slots.alpacaPaper !== -1 ? rowKeys[slots.alpacaPaper] : PENDING_ALPACA_PAPER,
          keySet: slots.alpacaPaper !== -1 && !!(setup?.credKeys ?? []).includes(draft.venues[slots.alpacaPaper].credentials),
          typed: secretDrafts[slots.alpacaPaper !== -1 ? draft.venues[slots.alpacaPaper].credentials : PENDING_ALPACA_PAPER] ?? { keyId: "", secret: "" },
          test: testState[slots.alpacaPaper !== -1 ? rowKeys[slots.alpacaPaper] : PENDING_ALPACA_PAPER],
          issue: slotIssues.alpacaPaper,
        }}
        live={{
          venue: slots.alpacaLive !== -1 ? draft.venues[slots.alpacaLive] : null,
          rowKey: slots.alpacaLive !== -1 ? rowKeys[slots.alpacaLive] : PENDING_ALPACA_LIVE,
          keySet: slots.alpacaLive !== -1 && !!(setup?.credKeys ?? []).includes(draft.venues[slots.alpacaLive].credentials),
          typed: secretDrafts[slots.alpacaLive !== -1 ? draft.venues[slots.alpacaLive].credentials : PENDING_ALPACA_LIVE] ?? { keyId: "", secret: "" },
          test: testState[slots.alpacaLive !== -1 ? rowKeys[slots.alpacaLive] : PENDING_ALPACA_LIVE],
          issue: slotIssues.alpacaLive,
        }}
        onTyped={(pendingOrCred, field, value) => setSecretField(pendingOrCred, field, value)}
        onTest={(idx, pendingKey, expectedEnv) => idx !== -1 ? void testExisting(idx) : void testPending(pendingKey, "alpaca", expectedEnv, true)}
        onRemove={(idx) => removeVenue(idx)}
        slots={slots}
        fieldWrap={fieldWrap}
      />

      <TradeZeroCard
        palette={palette}
        venue={slots.tradezero !== -1 ? draft.venues[slots.tradezero] : null}
        keySet={slots.tradezero !== -1 && !!(setup?.credKeys ?? []).includes(draft.venues[slots.tradezero].credentials)}
        typed={secretDrafts[slots.tradezero !== -1 ? draft.venues[slots.tradezero].credentials : PENDING_TRADEZERO] ?? { keyId: "", secret: "" }}
        test={testState[slots.tradezero !== -1 ? rowKeys[slots.tradezero] : PENDING_TRADEZERO]}
        issue={slotIssues.tradezero}
        onTyped={(field, value) => setSecretField(slots.tradezero !== -1 ? draft.venues[slots.tradezero].credentials : PENDING_TRADEZERO, field, value)}
        onTest={() => slots.tradezero !== -1 ? void testExisting(slots.tradezero) : void testPending(PENDING_TRADEZERO, "tradezero", "live", false)}
        onSelectAccount={(accId) => slots.tradezero !== -1 && patchVenue(slots.tradezero, { accountId: accId })}
        onRemove={slots.tradezero !== -1 ? () => removeVenue(slots.tradezero) : undefined}
        caps={slots.tradezero !== -1 ? (capsByRow[rowKeys[slots.tradezero]] ?? zeroCaps()) : zeroCaps()}
        limitsExpanded={slots.tradezero !== -1 ? (limitsOpen[rowKeys[slots.tradezero]] ?? false) : false}
        onToggleLimits={() => { const k = rowKeys[slots.tradezero]; setLimitsOpen((s) => ({ ...s, [k]: !(s[k] ?? false) })); }}
        onUpdateCap={(cap, value) => updateCap(rowKeys[slots.tradezero], cap, value)}
        fieldWrap={fieldWrap} groupLabel={groupLabel}
      />

      {overflow.length > 0 && (
        <div data-testid="other-venues" style={{ marginTop: 4, marginBottom: 16 }}>
          <div className="col-head" style={{ marginBottom: 6 }}>Other venues</div>
          {overflow.map((i) => {
            const v = draft.venues[i];
            return (
              <div key={i} data-testid={`other-venue-${i}`} style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 10px", border: `1px solid ${palette.border}`, borderRadius: 4, marginBottom: 6 }}>
                <span className="mono" style={{ fontWeight: 600 }}>{v.id}</span>
                <span style={{ color: palette.textMuted }}>{BROKER_LABEL[v.broker] ?? v.broker}</span>
                <span className="chip" style={{ color: palette.textMuted }}>{v.env.toUpperCase()}</span>
                <span style={{ flex: 1 }} />
                <Button variant="danger" confirm confirmLabel="Confirm remove" data-testid={`other-venue-remove-${i}`} onClick={() => removeVenue(i)}>
                  Remove
                </Button>
              </div>
            );
          })}
        </div>
      )}

      <div style={{ marginTop: 16, marginBottom: 16 }}>
        <div className="serif" style={{ fontSize: 14, fontWeight: 600, marginBottom: 8 }}>Global limits</div>
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
          {GLOBAL_CAPS.map((k) => (
            <label key={k} style={fieldWrap}>
              {k}
              <input className="field mono" data-testid={`global-${k}`} value={String(draft.gate.global[k])}
                onChange={(e) => { setDraft((d) => ({ ...d, gate: { ...d.gate, global: { ...d.gate.global, [k]: Number(e.target.value) || 0 } } })); setDirty(true); }}
                style={{ width: 90 }} />
            </label>
          ))}
        </div>
      </div>

      <div style={{ display: "flex", justifyContent: "flex-end" }}>
        <Button variant="primary" size="md" data-testid="save-venues" disabled={hasErrors} onClick={() => void saveVenues()}>
          Save venues & limits
        </Button>
      </div>
    </div>
  );
}

// ---- shared card shell ----------------------------------------------------

function CardHeader({ title, children }: { title: string; children?: ReactNode }): JSX.Element {
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "8px 12px", borderBottom: "1px solid var(--border)" }}>
      <span className="serif" style={{ fontWeight: 600 }}>{title}</span>
      <span style={{ flex: 1 }} />
      {children}
    </div>
  );
}

// bronze = configured + healthy, danger = live env, none = unconfigured —
// the only three states a card's top stripe (global.css .broker-card*) ever
// carries (§D visual system).
function stripeClass(hasVenue: boolean, isLive: boolean): string {
  if (!hasVenue) return "";
  return isLive ? "broker-card-live" : "broker-card-ok";
}

function RiskLimits({ caps, expanded, onToggle, onUpdateCap, fieldWrap, groupLabel, testIdPrefix }: {
  caps: GateLimitsView; expanded: boolean; onToggle: () => void;
  onUpdateCap: (cap: (typeof GATE_CAPS)[number], value: number) => void;
  fieldWrap: object; groupLabel: object; testIdPrefix: string;
}): JSX.Element {
  const setCount = GATE_CAPS.filter((c) => (caps[c] ?? 0) > 0).length;
  return (
    <div>
      <Button variant="quiet" data-testid={`${testIdPrefix}-limits-toggle`} onClick={onToggle}>
        {expanded ? "▾ " : "▸ "}
        {setCount > 0 ? `Risk limits · ${setCount} set` : "Configure risk limits"}
      </Button>
      {expanded && (
        <div style={{ marginTop: 8 }}>
          <div className="col-head" style={groupLabel}>Risk limits</div>
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "flex-end" }}>
            {GATE_CAPS.map((cap) => (
              <label key={cap} style={fieldWrap}>
                {cap}
                <input className="field mono" value={String(caps[cap] ?? 0)}
                  onChange={(e) => onUpdateCap(cap, Number(e.target.value) || 0)}
                  style={{ width: 72 }} />
              </label>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

// ---- Simulator -------------------------------------------------------------

function SimulatorCard({ palette, venue, running, rowKey, caps, limitsExpanded, onToggleLimits, onUpdateCap, onPatch, onAdd, onReset, fieldWrap, groupLabel }: {
  palette: Palette; venue: Venue | null; running: boolean; rowKey: string | undefined;
  caps: GateLimitsView; limitsExpanded: boolean; onToggleLimits: () => void;
  onUpdateCap: (cap: (typeof GATE_CAPS)[number], value: number) => void;
  onPatch: (over: Partial<Venue>) => void; onAdd: () => void; onReset: () => void;
  fieldWrap: object; groupLabel: object;
}): JSX.Element {
  return (
    <div className={`broker-card ${stripeClass(!!venue, false)}`} data-testid="sim-card">
      <CardHeader title="Simulator">
        {venue && <span className="chip chip-set">CONFIGURED</span>}
      </CardHeader>
      <div style={{ padding: 12, display: "flex", flexDirection: "column", gap: 12 }}>
        {!venue ? (
          <>
            <p style={{ fontSize: 12, color: palette.textMuted, margin: 0 }}>
              Practice venue with simulated fills — no real money.
            </p>
            <div>
              <Button variant="primary" data-testid="sim-add" onClick={onAdd}>Add simulator</Button>
            </div>
          </>
        ) : (
          <>
            <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "flex-end" }}>
              <label style={fieldWrap}>
                starting balance
                <input className="field mono" type="number" data-testid="sim-startingbalance"
                  value={String(venue.startingBalance ?? 0)}
                  onChange={(e) => onPatch({ startingBalance: Number(e.target.value) || 0 })}
                  style={{ width: 110 }} />
              </label>
              <label style={fieldWrap}>
                slippage bps
                <input className="field mono" type="number" data-testid="sim-slippage"
                  value={String(venue.slippageBps ?? 0)}
                  onChange={(e) => onPatch({ slippageBps: Number(e.target.value) || 0 })}
                  style={{ width: 90 }} />
              </label>
              <label style={fieldWrap}>
                fill latency (ms)
                <input className="field mono" type="number" data-testid="sim-filllatency"
                  value={String(venue.fillLatencyMs ?? 0)}
                  onChange={(e) => onPatch({ fillLatencyMs: Number(e.target.value) || 0 })}
                  style={{ width: 90 }} />
              </label>
              {running && (
                <Button variant="danger" confirm confirmLabel="Confirm reset" data-testid="sim-reset" onClick={onReset}>
                  Reset balance
                </Button>
              )}
            </div>
            {rowKey && (
              <RiskLimits caps={caps} expanded={limitsExpanded} onToggle={onToggleLimits} onUpdateCap={onUpdateCap}
                fieldWrap={fieldWrap} groupLabel={groupLabel} testIdPrefix="sim" />
            )}
          </>
        )}
      </div>
    </div>
  );
}

// ---- moomoo ----------------------------------------------------------------

function MoomooCard({
  palette, venue, fileHasIt, runningStatus, attempted, linkUp, probe, probing,
  selectedAccount, onSelectAccount, onProbe, onEnable, onRemove,
  rowKey, caps, limitsExpanded, onToggleLimits, onUpdateCap, fieldWrap, groupLabel,
}: {
  palette: Palette; venue: Venue | null; fileHasIt: boolean;
  runningStatus: { connected: boolean; note: string } | undefined;
  attempted: boolean; linkUp: boolean;
  probe: TestConnectionResult | null; probing: boolean;
  selectedAccount: string; onSelectAccount: (id: string) => void;
  onProbe: () => void; onEnable: () => void; onRemove: (() => void) | undefined;
  rowKey: string | undefined; caps: GateLimitsView; limitsExpanded: boolean;
  onToggleLimits: () => void; onUpdateCap: (cap: (typeof GATE_CAPS)[number], value: number) => void;
  fieldWrap: object; groupLabel: object;
}): JSX.Element {
  return (
    <div className={`broker-card ${stripeClass(!!venue, true)}`} data-testid="moomoo-card">
      <CardHeader title="moomoo">
        {venue && <span className="chip chip-live" data-testid="moomoo-live-chip">LIVE</span>}
        {venue && runningStatus && (
          <span className={`chip ${runningStatus.connected ? "chip-set" : "chip-live"}`} data-testid="moomoo-connection-chip">
            {runningStatus.connected ? "Connected" : "Disconnected"}
          </span>
        )}
      </CardHeader>
      <div style={{ padding: 12, display: "flex", flexDirection: "column", gap: 12 }}>
        {!venue && (
          probe?.ok ? (
            <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              <p style={{ fontSize: 12, color: palette.textMuted, margin: 0 }}>
                {probe.accounts.length > 1
                  ? "OpenD reports more than one live account. Pick the account eTape should trade through."
                  : `OpenD found one live-authorized account (${probe.accountId}).`}
              </p>
              {probe.accounts.length > 1 && (
                <select className="field" data-testid="moomoo-account-select" value={selectedAccount}
                  onChange={(e) => onSelectAccount(e.target.value)} style={{ width: 200 }}>
                  <option value="">select account</option>
                  {probe.accounts.map((a) => <option key={a.accountId} value={a.accountId}>{a.accountId} · {a.accountType}</option>)}
                </select>
              )}
              <div>
                <Button variant="primary" data-testid="moomoo-enable" disabled={!selectedAccount} onClick={onEnable}>
                  Enable moomoo
                </Button>
              </div>
            </div>
          ) : (
            <>
              <p style={{ fontSize: 12, color: palette.textMuted, margin: 0 }} data-testid="moomoo-body">
                {probe && !probe.ok
                  ? (probe.message || "No live US-authorized account found on this OpenD login.")
                  : attempted
                    ? "No live US-authorized account found on this OpenD login."
                    : "Waiting for OpenD — start the OpenD gateway and moomoo configures itself."}
              </p>
              {(attempted || linkUp) && (
                <div>
                  <Button data-testid="moomoo-probe" loading={probing} onClick={onProbe}>
                    {probing ? "Checking…" : "Check OpenD"}
                  </Button>
                </div>
              )}
            </>
          )
        )}
        {venue && (
          <>
            <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center" }}>
              <span className="mono" data-testid="moomoo-account">{venue.accountId}</span>
              {fileHasIt && !runningStatus && (
                <span className="chip chip-pending" data-testid="moomoo-badge-pending">Auto-configured — restart to activate</span>
              )}
              {onRemove && (
                <Button variant="danger" confirm confirmLabel="Confirm remove" data-testid="moomoo-remove" onClick={onRemove}>
                  Remove
                </Button>
              )}
            </div>
            {runningStatus && !runningStatus.connected && runningStatus.note && (
              <div style={{ ...{ color: palette.danger, fontSize: 10 } }}>{runningStatus.note}</div>
            )}
            <div className="broker-card-caveat" data-testid="moomoo-caveat">
              Day P&amp;L unavailable — the max-day-loss breaker does not see moomoo losses.
            </div>
            {rowKey && (
              <RiskLimits caps={caps} expanded={limitsExpanded} onToggle={onToggleLimits} onUpdateCap={onUpdateCap}
                fieldWrap={fieldWrap} groupLabel={groupLabel} testIdPrefix="moomoo" />
            )}
          </>
        )}
      </div>
    </div>
  );
}

// ---- credential slot (shared by Alpaca's two slots and TradeZero) --------

interface CredentialSlotProps {
  label?: string;
  venue: Venue | null;
  keySet: boolean;
  typed: SecretDraft;
  test: TestState | undefined;
  issue: string | undefined;
  onTyped: (field: "keyId" | "secret", value: string) => void;
  onTest: () => void;
  onRemove: (() => void) | undefined;
  caption?: ReactNode;
  accountArea?: ReactNode;
  testIdPrefix: string;
  fieldWrap: object;
}

function CredentialSlot({ label, venue, keySet, typed, test, issue, onTyped, onTest, onRemove, caption, accountArea, testIdPrefix, fieldWrap }: CredentialSlotProps): JSX.Element {
  return (
    <div>
      {label && <div className="col-head" style={{ marginBottom: 6 }}>{label}</div>}
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "flex-end" }}>
        <label style={fieldWrap}>
          key id
          <input type="password" autoComplete="off" className="field" data-testid={`${testIdPrefix}-keyid`}
            value={typed.keyId} onChange={(e) => onTyped("keyId", e.target.value)}
            placeholder="•••• (masked)" style={{ width: 150 }} />
        </label>
        <label style={fieldWrap}>
          secret
          <input type="password" autoComplete="off" className="field" data-testid={`${testIdPrefix}-secret`}
            value={typed.secret} onChange={(e) => onTyped("secret", e.target.value)}
            placeholder="•••• (masked)" style={{ width: 180 }} />
        </label>
        <span className={`chip ${keySet ? "chip-set" : ""}`} data-testid={`${testIdPrefix}-chip`}>
          {keySet ? "Key saved" : "no key"}
        </span>
        <Button data-testid={`${testIdPrefix}-test`} loading={test?.status === "testing"} onClick={onTest}>
          {test?.status === "testing" ? "Testing…" : "Test connection"}
        </Button>
        {venue && onRemove && (
          <Button variant="danger" confirm confirmLabel="Confirm remove" data-testid={`${testIdPrefix}-remove`} onClick={onRemove}>
            Remove
          </Button>
        )}
      </div>
      {accountArea}
      <div style={{ color: "var(--text-muted)", fontSize: 10, marginTop: 4 }}>leave blank to keep the existing key</div>
      {test?.message && (
        <div data-testid={`${testIdPrefix}-test-result`} style={{ color: test.status === "ok" ? "var(--up)" : "var(--danger)", fontSize: 10, marginTop: 2 }}>
          {test.status === "ok" ? "✓ " : "✗ "}{test.message}
        </div>
      )}
      {issue && <div data-testid={`${testIdPrefix}-issue`} style={{ color: "var(--danger)", fontSize: 10, marginTop: 2 }}>{issue}</div>}
      {caption && <div style={{ color: "var(--text-muted)", fontSize: 10.5, marginTop: 6 }}>{caption}</div>}
    </div>
  );
}

// ---- Alpaca -----------------------------------------------------------------

interface AlpacaSlotData {
  venue: Venue | null; rowKey: string; keySet: boolean; typed: SecretDraft;
  test: TestState | undefined; issue: string | undefined;
}

function AlpacaCard({ paper, live, onTyped, onTest, onRemove, slots, fieldWrap }: {
  paper: AlpacaSlotData; live: AlpacaSlotData;
  onTyped: (pendingOrCred: string, field: "keyId" | "secret", value: string) => void;
  onTest: (idx: number, pendingKey: string, expectedEnv: string) => void;
  onRemove: (idx: number) => void;
  slots: Slots; fieldWrap: object;
}): JSX.Element {
  const anyLive = live.venue != null;
  const anyConfigured = paper.venue != null || live.venue != null;
  return (
    <div className={`broker-card ${stripeClass(anyConfigured, anyLive)}`} data-testid="alpaca-card">
      <CardHeader title="Alpaca">
        {paper.venue && <span className="chip chip-set" data-testid="alpaca-paper-configured-chip">PAPER</span>}
        {live.venue && <span className="chip chip-live" data-testid="alpaca-live-configured-chip">LIVE</span>}
      </CardHeader>
      <div style={{ padding: 12, display: "flex", flexDirection: "column", gap: 16 }}>
        <CredentialSlot
          label="PAPER" venue={paper.venue} keySet={paper.keySet} typed={paper.typed} test={paper.test} issue={paper.issue}
          onTyped={(field, value) => onTyped(paper.venue ? paper.venue.credentials : PENDING_ALPACA_PAPER, field, value)}
          onTest={() => onTest(slots.alpacaPaper, PENDING_ALPACA_PAPER, "paper")}
          onRemove={paper.venue ? () => onRemove(slots.alpacaPaper) : undefined}
          caption="Also powers 1-minute chart history — worth adding even if you never trade here."
          testIdPrefix="alpaca-paper" fieldWrap={fieldWrap}
        />
        <CredentialSlot
          label="LIVE" venue={live.venue} keySet={live.keySet} typed={live.typed} test={live.test} issue={live.issue}
          onTyped={(field, value) => onTyped(live.venue ? live.venue.credentials : PENDING_ALPACA_LIVE, field, value)}
          onTest={() => onTest(slots.alpacaLive, PENDING_ALPACA_LIVE, "live")}
          onRemove={live.venue ? () => onRemove(slots.alpacaLive) : undefined}
          caption="Real-money account. Orders require the master arm switch."
          testIdPrefix="alpaca-live" fieldWrap={fieldWrap}
        />
      </div>
    </div>
  );
}

// ---- TradeZero --------------------------------------------------------------

function TradeZeroCard({
  palette, venue, keySet, typed, test, issue, onTyped, onTest, onSelectAccount, onRemove,
  caps, limitsExpanded, onToggleLimits, onUpdateCap, fieldWrap, groupLabel,
}: {
  palette: Palette; venue: Venue | null; keySet: boolean; typed: SecretDraft;
  test: TestState | undefined; issue: string | undefined;
  onTyped: (field: "keyId" | "secret", value: string) => void; onTest: () => void;
  onSelectAccount: (accountId: string) => void; onRemove: (() => void) | undefined;
  caps: GateLimitsView; limitsExpanded: boolean; onToggleLimits: () => void;
  onUpdateCap: (cap: (typeof GATE_CAPS)[number], value: number) => void;
  fieldWrap: object; groupLabel: object;
}): JSX.Element {
  const isLive = venue ? venue.env === "live" : false;
  const accounts = test?.accounts ?? [];
  const accountArea = venue && (
    accounts.length > 1 ? (
      <label style={{ ...fieldWrap, marginTop: 8 }}>
        account id
        <select className="field" data-testid="tz-account-select" value={venue.accountId}
          onChange={(e) => onSelectAccount(e.target.value)} style={{ width: 160 }}>
          <option value="">select account</option>
          {accounts.map((a) => <option key={a.accountId} value={a.accountId}>{a.accountId} · {a.accountType}</option>)}
        </select>
      </label>
    ) : (
      <div style={{ marginTop: 8, fontSize: 10.5, color: palette.textMuted }}>
        account id: <span className="mono" data-testid="tz-account-detected">{venue.accountId || "—"}</span>
      </div>
    )
  );
  return (
    <div className={`broker-card ${stripeClass(!!venue, isLive)}`} data-testid="tz-card">
      <CardHeader title="TradeZero">
        {venue && <span className={`chip ${isLive ? "chip-live" : ""}`} data-testid="tz-env-chip">{venue.env.toUpperCase()}</span>}
      </CardHeader>
      <div style={{ padding: 12, display: "flex", flexDirection: "column", gap: 12 }}>
        <CredentialSlot
          venue={venue} keySet={keySet} typed={typed} test={test} issue={issue}
          onTyped={onTyped} onTest={onTest} onRemove={onRemove}
          accountArea={accountArea}
          testIdPrefix="tz" fieldWrap={fieldWrap}
        />
        {venue && (
          <RiskLimits caps={caps} expanded={limitsExpanded} onToggle={onToggleLimits} onUpdateCap={onUpdateCap}
            fieldWrap={fieldWrap} groupLabel={groupLabel} testIdPrefix="tz" />
        )}
      </div>
    </div>
  );
}
