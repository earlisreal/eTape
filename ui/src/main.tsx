import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./fonts.css";
import "./global.css";
import { App } from "./App";
import { parseWorkspaceName } from "./chrome/windows";

const workspaceName = parseWorkspaceName(location.search);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App workspaceName={workspaceName} />
  </StrictMode>,
);
