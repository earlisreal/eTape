import { useEffect, useState } from "react";
import { useTheme } from "./ThemeProvider";
import type { ReplayCommandAdapter } from "./exec/useReplayCommands";
import { useReplayCommands } from "./exec/useReplayCommands";

const SPEEDS = [1, 2, 4, 0]; // 0 = max

type Pending = "demo" | "replay" | null;
type LaunchError = { section: "demo" | "replay"; message: string } | null;

// Unified "Practice" launcher (Task 5/U3, renamed from ReplayLauncherModal).
// The very first choice on entering a practice session is WHICH kind of
// practice — a synthetic demo market (zero setup, always available) or
// replaying a recorded day (needs at least one recorded day, plus a
// day/speed pick). Both are laid out as always-visible sections rather than
// a tab/segmented toggle: hiding the zero-config demo path behind an extra
// click would penalize the most frictionless option, and the two sections'
// natural heights already differ enough (one button vs. two selects + a
// button) that tabs would just spend a click equalizing them for no benefit.
// Each section's left accent stripe echoes the color of the banner you'll
// see once inside that mode (palette.demo <-> DemoBanner, palette.warn <->
// ReplayBanner), so the choice made here visually predicts what comes next.
//
// data-testid="replay-launcher" on the outer modal is kept from the
// pre-unification name deliberately, not an oversight — e2e/replay-launcher
// .spec.ts and AppShell.test.tsx's venue-setup-suppression tests key off it,
// and it's still an accurate handle (this IS the modal the "Practice" button
// opens; the id is a stable hook, not a description of its current content).
export function PracticeLauncherModal({ open, onClose, commands }: {
  open: boolean; onClose: () => void; commands: ReplayCommandAdapter;
}): JSX.Element | null {
  const { palette } = useTheme();
  const rc = useReplayCommands(commands);
  const [days, setDays] = useState<string[]>([]);
  const [day, setDay] = useState("");
  const [speed, setSpeed] = useState(1);
  const [pending, setPending] = useState<Pending>(null);
  const [error, setError] = useState<LaunchError>(null);

  useEffect(() => {
    if (!open) return;
    let live = true;
    rc.listDays().then((d) => { if (live) { setDays(d); setDay(d[0] ?? ""); } });
    return () => { live = false; };
  }, [open, rc]);

  // Every reopen starts clean — a stale error/pending flag from a previous
  // rejected attempt shouldn't bleed into the next time the modal is opened.
  useEffect(() => {
    if (open) { setError(null); setPending(null); }
  }, [open]);

  if (!open) return null;

  // Mirrors AppShell's onGoLive fix from Task 8: don't assume the promise
  // resolving means the command was accepted (check ack.status), and don't
  // let a rejection OR a transport failure close the modal out from under
  // the user — leave it open with an inline error so they can see what
  // happened and retry, instead of silently closing as if the session
  // started. `error` still tracks which section it belongs to (so a demo
  // failure doesn't paint a message under the replay section), but `pending`
  // gates BOTH start buttons regardless of which section set it: StartDemo
  // and StartReplay each trigger an engine self-restart, and letting one
  // section's button stay enabled while the other section's request is
  // still outstanding would let a user fire both concurrently — overwriting
  // `pending`, re-enabling a button whose request hasn't resolved yet, and
  // racing which `.then` gets to call onClose()/clear "Starting…" first.
  // (Reviewed/fixed post-Task 5 — see task-5-report.md.)
  const onStartDemo = () => {
    setPending("demo");
    setError(null);
    rc.startDemo().then((ack) => {
      if (ack.status !== "accepted") {
        setError({ section: "demo", message: ack.reason || "Start demo rejected" });
        setPending(null);
        return;
      }
      onClose();
    }).catch((err: unknown) => {
      setError({ section: "demo", message: err instanceof Error ? err.message : "Start demo failed" });
      setPending(null);
    });
  };

  const onStartReplay = () => {
    setPending("replay");
    setError(null);
    rc.start(day, speed).then((ack) => {
      if (ack.status !== "accepted") {
        setError({ section: "replay", message: ack.reason || "Start replay rejected" });
        setPending(null);
        return;
      }
      onClose();
    }).catch((err: unknown) => {
      setError({ section: "replay", message: err instanceof Error ? err.message : "Start replay failed" });
      setPending(null);
    });
  };

  return (
    <div onClick={onClose} style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.5)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 10000 }}>
      <div data-testid="replay-launcher" onClick={(e) => e.stopPropagation()} style={{ background: palette.surface, border: `1px solid ${palette.borderStrong}`, borderRadius: 6, width: 440, padding: 20 }}>
        <h3 style={{ marginTop: 0, marginBottom: 4 }}>Practice</h3>
        <p style={{ marginTop: 0, marginBottom: 18, color: palette.textMuted, fontSize: 12 }}>
          Nothing here touches real orders.
        </p>

        <div style={{ borderLeft: `3px solid ${palette.demo}`, paddingLeft: 12, marginBottom: 20 }}>
          <div style={{ fontWeight: 600, fontSize: 12, letterSpacing: ".04em", textTransform: "uppercase", color: palette.demo }}>
            Synthetic demo market
          </div>
          <p style={{ margin: "4px 0 10px", color: palette.textMuted, fontSize: 12 }}>
            A fictional, always-on market for drilling order flow and hotkeys — no history required.
          </p>
          {error?.section === "demo" && <p style={{ color: palette.danger, fontSize: 12, marginTop: 0 }}>{error.message}</p>}
          <div style={{ display: "flex", justifyContent: "flex-end" }}>
            <button data-testid="demo-start" disabled={pending !== null} onClick={onStartDemo}>
              {pending === "demo" ? "Starting…" : "Start demo market"}
            </button>
          </div>
        </div>

        <div style={{ borderLeft: `3px solid ${palette.warn}`, paddingLeft: 12, marginBottom: 18 }}>
          <div style={{ fontWeight: 600, fontSize: 12, letterSpacing: ".04em", textTransform: "uppercase", color: palette.warn }}>
            Replay a recorded day
          </div>
          <p style={{ margin: "4px 0 10px", color: palette.textMuted, fontSize: 12 }}>
            Rehearse a real session at your own pace.
          </p>
          {days.length === 0 ? (
            <p style={{ color: palette.textMuted, fontSize: 12 }}>No recorded days yet.</p>
          ) : (
            <>
              <label style={{ display: "block", marginBottom: 12 }}>Day
                <select data-testid="replay-day" value={day} onChange={(e) => setDay(e.target.value)} style={{ width: "100%" }}>
                  {days.map((d) => <option key={d} value={d}>{d}</option>)}
                </select>
              </label>
              <label style={{ display: "block", marginBottom: 12 }}>Speed
                <select data-testid="replay-speed" value={speed} onChange={(e) => setSpeed(Number(e.target.value))} style={{ width: "100%" }}>
                  {SPEEDS.map((s) => <option key={s} value={s}>{s === 0 ? "Max" : `${s}×`}</option>)}
                </select>
              </label>
              {error?.section === "replay" && <p style={{ color: palette.danger, fontSize: 12 }}>{error.message}</p>}
              <div style={{ display: "flex", justifyContent: "flex-end" }}>
                <button data-testid="replay-start" disabled={!day || pending !== null} onClick={onStartReplay}>
                  {pending === "replay" ? "Starting…" : "Start replay"}
                </button>
              </div>
            </>
          )}
        </div>

        <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
          <button onClick={onClose}>Cancel</button>
        </div>
      </div>
    </div>
  );
}
