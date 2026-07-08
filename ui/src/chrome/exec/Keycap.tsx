// The keycap chip: a normalized combo ("Ctrl+Shift+K") rendered as physical
// key caps. The one new visual primitive of the settings redesign — derived
// entirely from existing palette tokens (surface fill, borderStrong border,
// mono face, a 1px thicker bottom border for the key-cap read).
import { useTheme } from "../ThemeProvider";

const KEY_LABEL: Record<string, string> = {
  Ctrl: "Ctrl", Alt: "⌥", Shift: "⇧", Meta: "⌘", Backspace: "⌫", Enter: "⏎", Escape: "Esc",
};

export function Keycap({ combo, danger }: { combo: string; danger?: boolean }): JSX.Element {
  const { palette } = useTheme();
  const keys = combo.split("+").filter(Boolean);
  const color = danger ? palette.danger : palette.text;
  const border = danger ? palette.danger : palette.borderStrong;
  return (
    <span role="group" style={{ display: "inline-flex", gap: 3, alignItems: "center" }}>
      {keys.map((k, i) => (
        <kbd key={i} className="mono" style={{
          fontSize: 11, lineHeight: "14px", padding: "1px 5px", background: palette.surface,
          color, border: `1px solid ${border}`, borderBottomWidth: 2, borderRadius: 3,
        }}>{KEY_LABEL[k] ?? k}</kbd>
      ))}
    </span>
  );
}
