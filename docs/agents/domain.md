# Domain Docs

Use this guidance when naming domain concepts or changing a design covered by a decision record.

## Before exploring, read these

- [Glossary](../../CONTEXT.md) for established terms.
- [Architecture decision records](../adr/) that cover the behavior being changed.

## File structure

eTape uses a single-context layout:

```
/
├── CONTEXT.md
├── docs/adr/
├── engine/
└── ui/
```

## Use the glossary's vocabulary

When output names a domain concept—in an issue title, refactor proposal, hypothesis, or test name—use the term defined in `CONTEXT.md`. Do not drift to synonyms the glossary explicitly avoids.

If a needed concept is absent, check whether existing terms fit and flag any terminology that needs a decision.

## Flag ADR conflicts

If proposed work contradicts an existing ADR, surface the conflict explicitly rather than silently overriding it.
