import { useEffect, useState } from "react";
import { useTheme } from "./ThemeProvider";
import type { AckMsg } from "../wire/contract";
import { modalTracker } from "./modalTracker";

export function PracticeLauncherModal({ open, onClose, commands }: {
  open: boolean;
  onClose: () => void;
  commands: { sendCommand(name: string, args: unknown): Promise<AckMsg> };
}): JSX.Element | null {
  const { palette } = useTheme();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setPending(false);
      setError(null);
    }
  }, [open]);
  useEffect(() => {
    if (!open) return;
    modalTracker.setOpen(true);
    return () => modalTracker.setOpen(false);
  }, [open]);

  if (!open) return null;

  const onStartDemo = () => {
    setPending(true);
    setError(null);
    commands.sendCommand("StartDemo", {}).then((ack) => {
      if (ack.status !== "accepted") {
        setError(ack.reason || "Start demo rejected");
        setPending(false);
        return;
      }
      onClose();
    }).catch((err: unknown) => {
      setError(err instanceof Error ? err.message : "Start demo failed");
      setPending(false);
    });
  };

  return (
    <div onClick={onClose} style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.5)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 10000 }}>
      <div data-testid="replay-launcher" onClick={(e) => e.stopPropagation()} style={{ background: palette.surface, border: `1px solid ${palette.borderStrong}`, borderRadius: 6, width: 440, padding: 20 }}>
        <h3 style={{ marginTop: 0, marginBottom: 4 }}>Practice</h3>
        <p style={{ marginTop: 0, marginBottom: 18, color: palette.textMuted, fontSize: 12 }}>
          Nothing here touches real orders.
        </p>
        <div style={{ borderLeft: `3px solid ${palette.demo}`, paddingLeft: 12, marginBottom: 18 }}>
          <div style={{ fontWeight: 600, fontSize: 12, letterSpacing: ".04em", textTransform: "uppercase", color: palette.demo }}>
            Synthetic demo market
          </div>
          <p style={{ margin: "4px 0 10px", color: palette.textMuted, fontSize: 12 }}>
            A fictional, always-on market for drilling order flow and hotkeys — no history required.
          </p>
          {error && <p style={{ color: palette.danger, fontSize: 12, marginTop: 0 }}>{error}</p>}
          <div style={{ display: "flex", justifyContent: "flex-end" }}>
            <button data-testid="demo-start" disabled={pending} onClick={onStartDemo}>
              {pending ? "Starting…" : "Start demo market"}
            </button>
          </div>
        </div>
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
          <button onClick={onClose}>Cancel</button>
        </div>
      </div>
    </div>
  );
}
