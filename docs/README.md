# Documentation

Current codebase guides only. Start with [external APIs](external-apis.md), [performance evidence](performance.md), [plans](plans/README.md), or [specifications](specs/README.md).

## Policy

- Keep claims aligned with code and link owning subsystem README.
- Update guides when flow, interfaces, dependencies, invariants, or operations change.
- Store new work plans under `plans/`; retain durable outcomes, not execution diaries.
- Never record credentials, account identifiers, balances, or live keys.

## Legacy recovery

Legacy documentation was removed by commit `41aa9993777cab4ea59e711775094c516032ebf2`. Its parent is authoritative archive.

```bash
git ls-tree -r --name-only 41aa9993777cab4ea59e711775094c516032ebf2^ docs
git show 41aa9993777cab4ea59e711775094c516032ebf2^:docs/<old-path>
git restore --source=41aa9993777cab4ea59e711775094c516032ebf2^ -- docs
```
