import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, rmSync, rmdirSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const port = process.argv[2] ?? process.env.ETAPE_UIHUB_PORT ?? "8686";
const e2eDir = path.dirname(fileURLToPath(import.meta.url));
const uiDir = path.resolve(e2eDir, "..");
const engineDir = path.resolve(uiDir, "..", "engine");
const go = process.platform === "win32" ? "go.exe" : "go";
const configDir = mkdtempSync(path.join(os.tmpdir(), "etape-e2e-config-"));
const configPath = path.join(configDir, "config.toml");
writeFileSync(configPath, '[[venue]]\nid = "sim-paper"\nbroker = "sim"\nenv = "paper"\n');
process.once("exit", () => {
  for (const file of [configPath, `${configPath}.bak`]) rmSync(file, { force: true });
  try { rmdirSync(configDir); } catch { /* retain unexpected files for inspection */ }
});

const buildCommand = process.platform === "win32" ? "cmd.exe" : "npm";
const buildArgs = process.platform === "win32" ? ["/d", "/c", "npm run build"] : ["run", "build"];
execFileSync(buildCommand, buildArgs, {
  cwd: uiDir,
  stdio: "inherit",
});

const engine = spawn(
  go,
  [
    "run",
    "./cmd/etape",
    "-demo",
    "-demo-seed",
    "1",
    "-no-open",
    "-config",
    configPath,
    "-dist",
    path.join(uiDir, "dist"),
  ],
  {
    cwd: engineDir,
    env: { ...process.env, ETAPE_UIHUB_PORT: port },
    stdio: "inherit",
  },
);

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.once(signal, () => engine.kill(signal));
}

engine.once("error", (error) => {
  console.error(`e2e: failed to start ${go}: ${error.message}`);
  process.exitCode = 1;
});
engine.once("exit", (code, signal) => {
  process.exitCode = code ?? (signal ? 1 : 0);
});
