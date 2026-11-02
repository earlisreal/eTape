import { createContext, useContext, type ReactNode } from "react";
import { createPortal } from "react-dom";

export const SettingsActionsContext = createContext<HTMLElement | null>(null);

export function SettingsActions({ children }: { children: ReactNode }): JSX.Element {
  const target = useContext(SettingsActionsContext);
  const actions = <div className="settings-save">{children}</div>;
  return target ? createPortal(actions, target) : actions;
}
