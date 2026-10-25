# UI Test Support

Shared Vitest setup, DOM/canvas shims, and helpers. Production modules consume no test dependencies. Run: `npm test`.

`run.mjs` checks every default Vitest test file outside `node_modules`, `.git`
and `e2e` against the configured projects before running tests. Missing or
multiply assigned files fail immediately. Existing projects keep their batches;
the `unit` fallback automatically includes files outside those explicit groups.
The runner discovers all projects, so adding a file or project needs no edit to
the `npm test` command.

Each `golden`, `ladder` and `tape` file runs in its own Node process for native
canvas isolation. Put tests needing that isolation in those projects. Playwright
remains separate under `npm run e2e`. Use `npx vitest run <file>` for a focused
run.
