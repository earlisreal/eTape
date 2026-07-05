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
    // second file requires it) — jsdom auto-loads it for any real
    // `<canvas>.getContext("2d")` call, so every file that mounts a real
    // canvas (golden-image tests, and the panel chrome tests that render an
    // actual <canvas> rather than mocking it — LadderPanel, TapePanel) runs
    // in its own child process; everything else keeps the faster default
    // (threads) pool.
    poolMatchGlobs: [
      ["**/test/golden/**", "forks"],
      ["**/chrome/panels/LadderPanel.test.tsx", "forks"],
      ["**/chrome/panels/TapePanel.test.tsx", "forks"],
    ],
  },
});
