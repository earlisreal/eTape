# Controls

Reusable low-frequency workspace controls. Inputs: settings/user actions; outputs: callbacks or typed commands. Keep market payloads out of component state. Test: `npx vitest run src/chrome/controls`.

Use `Button` for neutral, primary, quiet, and destructive actions. `Dialog`
portals modal surfaces to the document body with one themed header, close
control, scrollable body, and optional footer. Outside clicks, Escape, and the
close control dismiss; a drag that starts inside does not dismiss on release
outside. Tab stays inside and closing returns focus to the opener. Set
`dismissible={false}` during a pending operation and disable its cancel action.

Anchored popups use `useDismissibleLayer` with their root ref and trigger as
`anchor`. Outside mouse/touch input dismisses; Escape returns focus to the
trigger. Only the frontmost registered surface handles dismissal. The hook
balances `modalTracker` registration on mount/unmount, including Strict Mode,
so scoped trading hotkeys and panel type-to-load stay quiet during these flows.
Register new interaction tests in `vitest.config.ts` so `npm test` runs them.
