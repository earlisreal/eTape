// The "General" settings page (settings unification): stacks four groups that
// used to live in separate sections — Theme (AppearanceSection), Sounds
// (SoundsSection), the ext-hours market order buffer % (moved out of
// OrderSettingsSection), and panel-layout backup (the "layout" half of
// BackupPanel). Every control saves immediately on change/step/blur, same as
// the sections it composes — there is no Save button on this page.
import { useState } from "react";
import { useTheme } from "./ThemeProvider";
import { AppearanceSection } from "./AppearanceSection";
import { SoundsSection } from "../sound/SoundsSection";
import { BackupPanel } from "./BackupPanel";
import { useOrderConfig } from "./exec/useOrderConfig";
import { StepField } from "./exec/StepField";
import { EXT_BUFFER_MIN, EXT_BUFFER_MAX, EXT_BUFFER_STEP } from "./exec/actionTemplate";
import type { ToastApi } from "./Toast";
import type { Workspace } from "./workspace";

export function GeneralSection(
  { getWorkspace, onImportWorkspace, toast }: {
    getWorkspace: () => Workspace;
    onImportWorkspace: (ws: Workspace) => void;
    toast: ToastApi;
  },
): JSX.Element {
  const { palette } = useTheme();
  const oc = useOrderConfig();
  // Only local state on this page: a staging value for the buffer field's
  // in-progress typed text (see StepField's own comment) — the displayed
  // numeric value itself is always re-derived from oc.config below, never
  // mirrored into useState, so it can't go stale after a save from elsewhere.
  const [rawEdit, setRawEdit] = useState<string | null>(null);
  const bufferPct = oc.config.extHoursMarketBufferPct ?? 1.0;

  const commit = (n: number) => {
    const clamped = Math.min(EXT_BUFFER_MAX, Math.max(EXT_BUFFER_MIN, Math.round(n * 10) / 10));
    oc.save({ ...oc.config, extHoursMarketBufferPct: clamped });
  };

  // Every group after the first gets a hairline top border + top spacing so
  // the stack reads as clearly separated groups, not one long list. The first
  // group (Theme) sits flush under the modal's own padding instead.
  const groupStyle = { borderTop: `1px solid ${palette.border}`, marginTop: 14, paddingTop: 14 };
  const headStyle = { marginBottom: 8 };
  const noteStyle = { fontSize: 12, color: palette.textMuted, marginBottom: 8 };

  return (
    <div style={{ color: palette.text }}>
      <AppearanceSection />

      <div style={groupStyle}>
        <SoundsSection />
      </div>

      <div style={groupStyle}>
        <div className="col-head serif" style={headStyle}>Extended hours</div>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span style={{ fontSize: 11, color: palette.text }}>Ext-hours market buffer %</span>
          <StepField
            ariaLabel="ext-buffer"
            testid="ext-buffer"
            value={rawEdit ?? String(bufferPct)}
            onType={(v) => {
              setRawEdit(v);
              const n = Number(v);
              if (!Number.isNaN(n)) commit(n);
            }}
            onStep={(dir) => { commit(bufferPct + dir * EXT_BUFFER_STEP); setRawEdit(null); }}
            onBlur={() => setRawEdit(null)}
            style={{ width: 84 }}
          />
        </div>
        <div style={{ ...noteStyle, marginTop: 6, marginBottom: 0 }}>Buffer added to market orders placed outside regular hours.</div>
      </div>

      <div style={groupStyle}>
        <div className="col-head serif" style={headStyle}>Layout</div>
        <div style={noteStyle}>Save this window's panel layout to a file, or restore it.</div>
        <BackupPanel part="layout" getWorkspace={getWorkspace} onImportWorkspace={onImportWorkspace} toast={toast} />
      </div>

      <div style={groupStyle}>
        <div className="col-head serif" style={headStyle}>Trading</div>
        <label style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <input type="checkbox" aria-label="auto-unlock-startup"
            checked={oc.config.autoUnlockOnStartup ?? false}
            onChange={(e) => oc.save({ ...oc.config, autoUnlockOnStartup: e.target.checked })} />
          Automatically unlock trading on startup
        </label>
        <div style={noteStyle}>Trading starts locked each session for safety. Enable to unlock it automatically once eTape connects.</div>
      </div>
    </div>
  );
}
