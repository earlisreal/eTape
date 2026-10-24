import { useTheme } from "./ThemeProvider";

export function AppearanceSection(): JSX.Element {
  const { mode, setMode } = useTheme();
  return (
    <div>
      <div className="col-head serif" style={{ marginBottom: 8 }}>Theme</div>
      <div className="theme-choices"><label className="theme-choice">
        <input type="radio" name="theme" aria-label="Light" checked={mode === "light"} onChange={() => setMode("light")} /> Light (default)
        <span className="theme-swatch" aria-hidden="true" style={{ background: "#FBFAF7", marginLeft: "auto" }} />
      </label>
      <label className="theme-choice">
        <input type="radio" name="theme" aria-label="Dark" checked={mode === "dark"} onChange={() => setMode("dark")} /> Dark
        <span className="theme-swatch" aria-hidden="true" style={{ background: "#14120E", marginLeft: "auto" }} />
      </label>
      </div>
    </div>
  );
}
