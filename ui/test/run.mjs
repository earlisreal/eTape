import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { globSync } from "node:fs";
import { dirname, relative, resolve } from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import { configDefaults } from "vitest/config";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const vitest = resolve(root, "node_modules/vitest/vitest.mjs");

function run(args, capture = false) {
  const result = spawnSync(process.execPath, [vitest, ...args], {
    cwd: root,
    encoding: "utf8",
    stdio: capture ? "pipe" : "inherit",
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    if (capture) {
      process.stdout.write(result.stdout ?? "");
      process.stderr.write(result.stderr ?? "");
    }
    process.exit(result.status ?? 1);
  }
  return result.stdout;
}

const specs = JSON.parse(run(["list", "--filesOnly", "--json"], true));
const expected = new Set([...globSync(configDefaults.include, {
  cwd: root,
  exclude: [...configDefaults.exclude, "e2e/**"],
})].map((file) => resolve(root, file)));
const scheduled = new Map();
const projects = new Map();
for (const { file, projectName } of specs) {
  const path = resolve(file);
  assert(!scheduled.has(path), `Test belongs to multiple projects: ${relative(root, path)}`);
  assert(expected.has(path), `Unexpected test outside Vitest inventory: ${relative(root, path)}`);
  assert(projectName, `Test has no named project: ${relative(root, path)}`);
  scheduled.set(path, projectName);
  if (!projects.has(projectName)) projects.set(projectName, []);
  projects.get(projectName).push(path);
}
const missing = [...expected].filter((file) => !scheduled.has(file));
assert.equal(missing.length, 0, `Unscheduled Vitest files:\n${missing.map((file) => relative(root, file)).join("\n")}`);
process.stdout.write(`UI test inventory: ${scheduled.size} files scheduled exactly once across ${projects.size} projects.\n`);

for (const [project, files] of projects) {
  if (["golden", "ladder", "tape"].includes(project)) {
    for (const file of files.sort()) run(["run", "--project", project, relative(root, file)]);
  } else {
    run(["run", "--project", project]);
  }
}
