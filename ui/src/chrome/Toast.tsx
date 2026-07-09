import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTheme } from "./ThemeProvider";

export type ToastLevel = "info" | "success" | "warn" | "danger";
export interface Toast { id: number; level: ToastLevel; text: string; sticky?: boolean }
export interface ToastApi { push(t: Omit<Toast, "id">): void; dismiss(id: number): void }

const Ctx = createContext<ToastApi | null>(null);

export function ToastProvider(
  { children, autoDismissMs = 4000 }: { children: ReactNode; autoDismissMs?: number },
): JSX.Element {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const seq = useRef(0);
  const timers = useRef(new Map<number, ReturnType<typeof setTimeout>>());

  const dismiss = useCallback((id: number) => {
    const handle = timers.current.get(id);
    if (handle !== undefined) {
      clearTimeout(handle);
      timers.current.delete(id);
    }
    setToasts((ts) => ts.filter((t) => t.id !== id));
  }, []);
  const push = useCallback((t: Omit<Toast, "id">) => {
    const id = ++seq.current;   // monotonic per-provider id
    setToasts((ts) => [...ts, { ...t, id }]);
    if (!t.sticky) timers.current.set(id, setTimeout(() => dismiss(id), autoDismissMs));
  }, [autoDismissMs, dismiss]);

  useEffect(() => {
    const map = timers.current;
    return () => {
      map.forEach((handle) => clearTimeout(handle));
      map.clear();
    };
  }, []);

  const api = useMemo<ToastApi>(() => ({ push, dismiss }), [push, dismiss]);
  return <Ctx.Provider value={api}><>{children}<ToastHost toasts={toasts} onDismiss={dismiss} /></></Ctx.Provider>;
}

export function useToasts(): ToastApi {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useToasts must be used within a ToastProvider");
  return ctx;
}

function ToastHost({ toasts, onDismiss }: { toasts: Toast[]; onDismiss: (id: number) => void }): JSX.Element {
  const { palette } = useTheme();
  const color = (l: ToastLevel) => (l === "danger" ? palette.danger : l === "warn" ? palette.warn : l === "success" ? palette.ok : palette.accent);
  return (
    <div style={{ position: "fixed", right: 12, bottom: 12, display: "flex", flexDirection: "column", gap: 6, zIndex: 9999, maxWidth: 380 }}>
      {toasts.map((t) => (
        <div key={t.id} role="alert" onClick={() => onDismiss(t.id)}
          onMouseEnter={(ev) => (ev.currentTarget.style.background = palette.border)}
          onMouseLeave={(ev) => (ev.currentTarget.style.background = palette.surface)}
          style={{ background: palette.surface, border: `1px solid ${color(t.level)}`, borderLeft: `4px solid ${color(t.level)}`,
            color: palette.text, padding: "6px 10px", fontSize: 12, borderRadius: 4, cursor: "pointer", boxShadow: "0 2px 8px rgba(0,0,0,.25)" }}>
          {t.text}
        </div>
      ))}
    </div>
  );
}
