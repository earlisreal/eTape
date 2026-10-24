import { GeneralSection } from "./GeneralSection";
import { AboutSection } from "./AboutSection";
import { OrderSettingsSection } from "./exec/OrderSettingsSection";
import { VenuesSection } from "./exec/VenuesSection";
import { BackupPanel } from "./BackupPanel";
import { useOrderConfig } from "./exec/useOrderConfig";
import { Button } from "./controls/Button";
import { Dialog } from "./controls/Dialog";
import type { AckMsg } from "../wire/contract";
import type { ToastApi } from "./Toast";
import type { Workspace } from "./workspace";
import type { ConnState } from "../wire/WsClient";
import type { HealthStore } from "../data/HealthStore";
import type { ExecStore } from "../data/ExecStore";
import type { SessionStore } from "../data/SessionStore";

export type SettingsSection = "general" | "orders" | "venues" | "about";
const NAV: { id: SettingsSection; label: string; detail: string; note: string }[] = [
  { id: "general", label: "General", detail: "Appearance, sound & workspace", note: "Make eTape feel right for you. Changes save as you go." },
  { id: "orders", label: "Orders & hotkeys", detail: "Templates, shortcuts & Deck", note: "Configure your trading controls. Save template edits before leaving this section." },
  { id: "venues", label: "Venues & creds", detail: "Brokers & connections", note: "Manage your broker connections. Test and save changes on each broker card." },
  { id: "about", label: "About", detail: "Version & diagnostics", note: "Application details and connection diagnostics." },
];

export function SettingsModal({ open, section, onSection, onClose, commands, getWorkspace, onImportWorkspace, toast, engineState, health, exec, session }:
  {
    open: boolean; section: SettingsSection; onSection: (s: SettingsSection) => void; onClose: () => void;
    commands: {
      sendCommand(name: string, args: unknown): Promise<AckMsg>;
      sendQuery?(name: string, args: unknown): Promise<unknown>;
    };
    getWorkspace: () => Workspace; onImportWorkspace: (ws: Workspace) => void; toast: ToastApi;
    // Optional (not required, unlike FeedStatusBanner's engineState) so
    // existing tests that render SettingsModal without it keep compiling;
    // `| undefined` is required alongside the `?` because tsconfig's
    // exactOptionalPropertyTypes otherwise rejects explicitly passing
    // `engineState={engineState}` when the caller's own value is
    // `ConnState | undefined` (AppShell's engineState is always defined in
    // practice, but its static type here is whatever this prop declares).
    engineState?: ConnState | undefined;
    // Threaded to VenuesSection's moomoo card (OpenD link status + live
    // connected/note per venue) — the app's single existing HealthStore/
    // ExecStore instances (see AppShell), not a new subscription. Optional
    // for the same reason as engineState above.
    health?: HealthStore | undefined;
    exec?: ExecStore | undefined;
    // Threaded to VenuesSection so it can suppress the restart banner while
    // in demo mode (see VenuesSection's own doc comment). Optional for the
    // same reason as health/exec above.
    session?: SessionStore | undefined;
  }): JSX.Element | null {
  const oc = useOrderConfig();
  const current = NAV.find((n) => n.id === section)!;
  if (!open) return null;
  return (
    <Dialog title="Settings" onClose={onClose} width={1000} className="settings-dialog" closeLabel="Close settings"
      footer={<><span className="dialog-hint">Esc or click outside to close</span><Button size="md" onClick={onClose}>Done</Button></>}>
        <nav className="settings-nav" aria-label="Settings sections">
          {NAV.map((n) => (
            <Button key={n.id} aria-label={n.label} aria-current={section === n.id ? "page" : undefined}
              onClick={() => onSection(n.id)} hoverStyle={{ background: "var(--bg)", color: "var(--text)" }}>
              <strong>{n.label}</strong><small>{n.detail}</small>
            </Button>
          ))}
        </nav>
        <section key={section} className="settings-content" aria-label={current.label}>
          <div className="settings-section-header"><h3>{current.label}</h3><p>{current.note}</p></div>
          {section === "general" && <GeneralSection getWorkspace={getWorkspace} onImportWorkspace={onImportWorkspace} toast={toast} />}
          {section === "orders" && (
            <>
              <OrderSettingsSection config={oc.config} onSave={oc.save} toast={toast} onClose={onClose}
                commands={commands} exec={exec} />
              <div style={{ borderTop: "1px solid var(--border)", marginTop: 14, paddingTop: 14 }}>
                <div className="col-head serif" style={{ marginBottom: 8 }}>Import & export hotkeys & deck</div>
                <BackupPanel part="hotkeys" orderConfig={oc.config} onImportOrderConfig={oc.save} toast={toast} />
              </div>
            </>
          )}
          {section === "venues" && <VenuesSection commands={commands} engineState={engineState} health={health} exec={exec} session={session} />}
          {section === "about" && <AboutSection commands={commands} />}
        </section>
    </Dialog>
  );
}
