import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev server proxies /ws to the engine (or the mock engine) on 127.0.0.1:8686.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/ws": { target: "ws://127.0.0.1:8686", ws: true },
    },
  },
});
