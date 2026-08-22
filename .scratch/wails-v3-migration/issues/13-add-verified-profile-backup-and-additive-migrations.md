# 13 — Add verified profile backup and additive migrations

**What to build:** Establish the verified backup and versioned migration framework that converts an existing user profile additively, idempotently, privately, and safely to retry after any failure.

**Blocked by:** 05 — Put engine lifecycle behind admission and drain; 10 — Make Workspace catalog and Native Window registry canonical.

**Status:** ready-for-agent

- [ ] Development, replay, prototypes, server tests, and automated migration tests default to isolated data roots; access to the real user profile requires an explicit migration run.
- [ ] Migration acquires the data-root and database integrity locks before inspection and before any normal writer can start.
- [ ] A timestamped backup is created and verified before migration writes, using a closed-store or SQLite-consistent method that correctly handles WAL and SHM state rather than copying an active database blindly.
- [ ] Ordered domain migrations register with one versioned framework, which advances a target-version marker only after every step registered for that target commits; later shared-state tickets add their conversions through this framework rather than bypassing it.
- [ ] Migration preserves Workspace layout version 8, Panel identities, catalog entries, settings, drawings, credentials, and engine/store data throughout every registered conversion.
- [ ] Re-running a current or interrupted migration is idempotent and never applies a completed transformation twice.
- [ ] Validation, backup, transaction, or disk failure aborts startup with the migration marker absent and leaves the source profile and verified backup usable; no automatic reset occurs.
- [ ] A clean reset is a separate explicit confirmation path that creates and verifies another backup before changing user data.
- [ ] Diagnostics and fixtures contain no credentials, symbols, account data, balances, keys, or other sensitive runtime payloads; logs identify only safe migration stages and identifiers.
- [ ] Redacted fixtures cover normal, current, already-migrated, corrupt, missing-optional-file, WAL/SHM, backup-failure, and disk-failure profiles, including backup readability and marker-last assertions.
- [ ] The migration and rollback guide explains the verified backup, failure recovery, explicit reset, and prior-build restore procedure without exposing real profile contents.
