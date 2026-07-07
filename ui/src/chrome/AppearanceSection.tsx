import { useTheme } from "./ThemeProvider";

export function AppearanceSection(): JSX.Element {
  const { mode, setMode } = useTheme();
  return (
    <div>
      <div className="col-head serif" style={{ marginBottom: 8 }}>Theme</div>
      <label style={{ display: "block", marginBottom: 6 }}>
        <input type="radio" name="theme" aria-label="Light" checked={mode === "light"} onChange={() => setMode("light")} /> Light (default)
      </label>
      <label style={{ display: "block" }}>
        <input type="radio" name="theme" aria-label="Dark" checked={mode === "dark"} onChange={() => setMode("dark")} /> Dark
      </label>
    </div>
  );
}
