import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// Default node environment for pure logic; chrome/*.test.tsx opt into jsdom
// per-file with `// @vitest-environment jsdom`.
export default defineConfig({
  plugins: [react()],
  test: {
    globals: true,
    environment: "node",
    // node-canvas's native addon isn't safe to load into more than one
    // worker thread per process ("Module did not self-register" once a
    // second golden test file requires it) — run just the golden test
    // files (which pull in node-canvas via the harness) in separate child
    // processes; everything else keeps the faster default (threads) pool.
    poolMatchGlobs: [["**/test/golden/**", "forks"]],
  },
});
