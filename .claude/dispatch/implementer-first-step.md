# Mandatory first step for worktree-based implementer/fix-subagent dispatch prompts

Paste this verbatim, filled in, as the **first section** of every implementer or
fix-subagent dispatch prompt in a worktree-based session — before any task
description, before "Work from: <dir>". A bare "Work from: <dir>" line is not
enough; subagents have repeatedly landed commits on shared `main` despite it.

A `commit-msg` hook (`.githooks/commit-msg`, wired via `core.hooksPath`) blocks
accidental commits on `main` as a backstop — but catching the mistake before any
file is touched is still cheaper than recovering from it, so keep using this.

---

## MANDATORY FIRST STEP — verify your working directory before touching anything

Before reading, editing, or running any other command, run:

```
pwd && git rev-parse --show-toplevel && git branch --show-current
```

Expected:
- `pwd` / `git rev-parse --show-toplevel` → `<absolute worktree path, e.g.
  /Users/earl.savadera/Projects/eTape/.claude/worktrees/<branch-name>>`
- `git branch --show-current` → `<expected worktree branch, e.g.
  worktree-<branch-name>>`

**If any value doesn't match exactly: STOP. Do not edit any file, do not run any
git command that mutates state, do not commit. Report back that you are BLOCKED
and why**, instead of proceeding or trying to fix it yourself.

---

Only once the check above passes should the rest of the task begin.
