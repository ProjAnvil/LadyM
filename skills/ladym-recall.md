---
name: ladym-recall
description: Use when you need to recall project memory or codebase context — instead of re-reading or grepping files, query LadyM with a keyword. Trigger phrases include "what do we know about X", "how does X work", "where is X defined", "remind me about", "recall", or whenever you're about to Read/Grep a file you've likely seen before in this workspace.
---

# LadyM Recall Skill

LadyM is a layered memory + codebase RAG system. This skill lets you pull memory and code
analysis into your context with one keyword, instead of `Read`/`Grep`-ing the same files
again.

## When to use

- Before reading or grepping a file you suspect you've already touched in this workspace.
- When the user asks "how does X work" / "where is X" / "what did we decide about X".
- When you need a function's signature, docstring, callers, or callees.
- When you want a previously stored playbook for a recurring task (deploy, debug, etc.).

## How to use

The CLI is the simplest entry point — no MCP wiring required.

### Recall (free-form query)

```bash
ladym recall "how does authentication work" --top-k 5
ladym recall "auth login flow" --code      # restrict to codebase analysis
ladym recall "deploy service" --json       # for programmatic consumption
```

### Index the current codebase first (only when stale)

```bash
ladym index ./src                # incremental — skips unchanged files
ladym index ./src --force        # full rebuild
```

### Store a new memory

```bash
ladym remember "auth uses JWT with 24h expiry" --tags auth,security
```

### Stats

```bash
ladym stats
```

## Output format

`ladym recall` prints a ranked table: `score | layer | type | summary | source`. Use the
`source` column (a file path for code items, an agent name for episodes) to decide whether to
drill in further with a targeted `Read`, or whether the retrieved summary is already enough.

For tier information: `tier=1` means the cheap vector+activation pass answered the query
(HyMem cognitive economy — ~70% of queries land here); `tier=2` means the deep graph
expansion ran because tier-1 self-reflection found coverage insufficient.

## When NOT to use

- The file is brand new and unindexed — index first with `ladym index`.
- The user explicitly wants you to read a specific file in full.
- You need byte-exact content (LadyM stores analysis, not raw bytes beyond the first ~40
  lines of each symbol body).

## Persistence

Memories live in `./ladym.db` (or `$LADYM_DB`). Each project gets its own DB by default.
Set `LADYM_WORKSPACE=yourteam` to isolate memories within a shared DB.
