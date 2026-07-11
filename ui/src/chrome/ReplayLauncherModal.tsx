import { useEffect, useState } from "react";
import { useTheme } from "./ThemeProvider";
import type { ReplayCommandAdapter } from "./exec/useReplayCommands";
import { useReplayCommands } from "./exec/useReplayCommands";

const SPEEDS = [1, 2, 4, 0]; // 0 = max

export function ReplayLauncherModal({ open, onClose, commands }: {
  open: boolean; onClose: () => void; commands: ReplayCommandAdapter;
}): JSX.Element | null {
  const { palette } = useTheme();
  const rc = useReplayCommands(commands);
  const [days, setDays] = useState<string[]>([]);
  const [day, setDay] = useState("");
  const [speed, setSpeed] = useState(1);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    let live = true;
    rc.listDays().then((d) => { if (live) { setDays(d); setDay(d[0] ?? ""); } });
    return () => { live = false; };
  }, [open, rc]);

  // Every reopen starts clean — a stale error/starting flag from a previous
  // rejected attempt shouldn't bleed into the next time the modal is opened.
  useEffect(() => {
    if (open) { setError(null); setStarting(false); }
  }, [open]);

  if (!open) return null;

  // Mirrors AppShell's onGoLive fix from Task 8: don't assume the promise
  // resolving means the command was accepted (check ack.status), and don't
  // let a rejection OR a transport failure close the modal out from under
  // the user — leave it open with an inline error so they can see what
  // happened and retry, instead of silently closing as if replay started.
  const onStart = () => {
    setStarting(true);
    setError(null);
    rc.start(day, speed).then((ack) => {
      if (ack.status !== "accepted") {
        setError(ack.reason || "Start replay rejected");
        setStarting(false);
        return;
      }
      onClose();
    }).catch((err: unknown) => {
      setError(err instanceof Error ? err.message : "Start replay failed");
      setStarting(false);
    });
  };

  return (
    <div onClick={onClose} style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.5)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 10000 }}>
      <div data-testid="replay-launcher" onClick={(e) => e.stopPropagation()} style={{ background: palette.surface, border: `1px solid ${palette.borderStrong}`, borderRadius: 6, width: 380, padding: 20 }}>
        <h3 style={{ marginTop: 0 }}>Practice: replay a recorded day</h3>
        {days.length === 0 ? (
          <p style={{ color: palette.textMuted }}>No recorded days yet.</p>
        ) : (
          <>
            <label style={{ display: "block", marginBottom: 12 }}>Day
              <select data-testid="replay-day" value={day} onChange={(e) => setDay(e.target.value)} style={{ width: "100%" }}>
                {days.map((d) => <option key={d} value={d}>{d}</option>)}
              </select>
            </label>
            <label style={{ display: "block", marginBottom: 16 }}>Speed
              <select data-testid="replay-speed" value={speed} onChange={(e) => setSpeed(Number(e.target.value))} style={{ width: "100%" }}>
                {SPEEDS.map((s) => <option key={s} value={s}>{s === 0 ? "Max" : `${s}×`}</option>)}
              </select>
            </label>
          </>
        )}
        {error && <p style={{ color: palette.danger, fontSize: 12 }}>{error}</p>}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
          <button onClick={onClose}>Cancel</button>
          <button data-testid="replay-start" disabled={!day || starting} onClick={onStart}>
            {starting ? "Starting…" : "Start replay"}
          </button>
        </div>
      </div>
    </div>
  );
}
