import "@wailsio/runtime";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
// dockview's own stylesheet must load BEFORE global.css: both define
// `--dv-*` custom properties on the same `.dockview-theme-light`/`-dark`
// selectors, and at equal specificity the later rule in source order wins.
// Importing it here (ahead of global.css) — rather than at its point of use
// in AppShell.tsx — guarantees our Daylight Ledger overrides always cascade
// last, regardless of how deep AppShell sits in the module graph.
import "dockview/dist/styles/dockview.css";
import "./fonts.css";
import "./global.css";
import { App } from "./App";
import { parseWorkspaceName, workspaceWindowTarget } from "./chrome/windows";
import { MONITORING_WORKSPACE_ID } from "./chrome/workspace";

const workspaceName = parseWorkspaceName(location.search);
if (workspaceName === MONITORING_WORKSPACE_ID) window.name = workspaceWindowTarget(MONITORING_WORKSPACE_ID);
else if (window.name === workspaceWindowTarget(MONITORING_WORKSPACE_ID)) window.name = "";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App workspaceName={workspaceName} />
  </StrictMode>,
);
