import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./global.css";
import { App } from "./App";

const params = new URLSearchParams(location.search);
const workspaceName = params.get("workspace") === "trading" ? "trading" : "monitoring";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App workspaceName={workspaceName} />
  </StrictMode>,
);
