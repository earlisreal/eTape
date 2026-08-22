# End-to-End Tests

Playwright scenarios exercise the browser against controlled engine/demo state.
The launcher creates a fresh temporary `server` profile for every run; it never
reads or writes `%USERPROFILE%\.eTape`. Inputs: fixtures and built services;
outputs: behavioral assertions/screenshots. Avoid live venues and
nondeterministic external feeds. Run: `npm run e2e`. The isolated ticketless
cross-window scenario uses the same deterministic sim-only harness:
`npm run e2e:ticketless`.
