// Shared test wrapper for components that read theme / order-config / sound-config
// context (Task 11+: SettingsModal and friends). OrderConfigProvider and
// SoundConfigProvider both require a non-optional `commands` prop and call
// `commands.sendCommand(...)` unconditionally on mount (a "GetConfig" fetch) — an
// empty-object stub would throw synchronously on mount, so this supplies a working
// stub that resolves with a benign "accepted" ack. ThemeProvider's `commands` is
// optional and guarded, so it doesn't strictly need one, but passing the same
// stub keeps this wrapper uniform and lets tests exercise theme persistence too.
//
// Deliberately does NOT include ToastProvider: it renders an always-present
// ToastHost div even with zero toasts, which would break the "returns null when
// closed" assertion (container.firstChild) that most SettingsModal tests rely on.
// The one test that renders the Venues & creds section (which calls useToasts())
// wraps itself in a local <ToastProvider> instead.
import type { ReactNode } from "react";
import { ThemeProvider } from "../chrome/ThemeProvider";
import { OrderConfigProvider } from "../chrome/exec/useOrderConfig";
import { SoundConfigProvider } from "../sound/SoundConfigProvider";
import type { AckMsg } from "../wire/contract";

const testCommands = {
  sendCommand: async (): Promise<AckMsg> => ({ kind: "ack", corrId: "test", status: "accepted", value: undefined }),
  sendQuery: async (): Promise<unknown> => ({}),
};

export function AppProviders({ children }: { children: ReactNode }): JSX.Element {
  return (
    <ThemeProvider commands={testCommands}>
      <OrderConfigProvider commands={testCommands}>
        <SoundConfigProvider commands={testCommands}>
          {children}
        </SoundConfigProvider>
      </OrderConfigProvider>
    </ThemeProvider>
  );
}
