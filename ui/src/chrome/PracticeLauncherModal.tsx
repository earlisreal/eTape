import { useEffect, useState } from "react";
import { useTheme } from "./ThemeProvider";
import type { AckMsg } from "../wire/contract";
import { Dialog } from "./controls/Dialog";
import { Button } from "./controls/Button";

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
    <Dialog title="Practice" onClose={onClose} dismissible={!pending} testId="practice-launcher"
      footer={<><span className="dialog-hint">A simulated market for practice</span><Button disabled={pending} onClick={onClose}>Cancel</Button></>}>
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
            <Button variant="primary" size="md" data-testid="demo-start" loading={pending} onClick={onStartDemo}>
              {pending ? "Starting…" : "Start demo market"}
            </Button>
          </div>
        </div>
    </Dialog>
  );
}
