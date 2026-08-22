# Prototypes

Research scripts and benchmark harnesses, not production runtime. Includes OpenD latency/cadence/quota probes, scanner validation, broker execution timing, the synthetic browser-host baseline fixture, and reference tick-to-10-second aggregation. The baseline fixture and protocol live under [browser-baseline](browser-baseline/README.md).

Inputs: explicitly configured APIs or sanitized captures. Outputs: console/JSON evidence under `captures/` (excluded from README leaves). Browser baseline and automated validation use isolated eTape profile roots; never point them at `%USERPROFILE%\\.eTape`. Never run order scripts against live keys without current explicit authorization. Historical methodology links: [performance evidence](../docs/performance.md). Run scripts individually with documented arguments; no aggregate test contract.
