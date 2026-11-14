# Documentation

Start with the [engine guide](../engine/README.md), [UI guide](../ui/README.md), [external APIs](external-apis.md), or [performance evidence](performance.md). Use [plans](plans/README.md) and [specifications](specs/README.md) for approved designs and [AGENTS.md](../AGENTS.md) for the repository workflow.

## Policy

- Keep claims aligned with code. Record non-obvious behavior, decisions, and operating procedures in the guide that owns the topic. Link to the relevant source or test for implementation details.
- Store new work plans under `plans/`; retain durable outcomes, not execution diaries.
- Never record credentials, account identifiers, balances, or live keys.

## Legacy recovery

Legacy documentation was removed by commit `41aa9993777cab4ea59e711775094c516032ebf2`. Its parent is authoritative archive.

```bash
git ls-tree -r --name-only 41aa9993777cab4ea59e711775094c516032ebf2^ docs
git show 41aa9993777cab4ea59e711775094c516032ebf2^:docs/<old-path>
git restore --source=41aa9993777cab4ea59e711775094c516032ebf2^ -- docs
```
