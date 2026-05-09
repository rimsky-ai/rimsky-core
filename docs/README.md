# Rimsky documentation

This directory hosts both the **public-documentation surface** (intended for external consumers and their coding agents) and the **internal/working surface** (engineering material that is unmaintained going forward and not intended for external citation).

## Public surface

- `concepts/` — canonical per-concept reference (one file per domain noun). See `concepts/design-philosophy.md` for the framing the rest of the surface is written under.
- `protocols/` — protocol-implementation guides (`ClaimProducer`, `Executor`, `LifecycleSubscriber`).
- `agents/` — agent-shaped indices (`llms.txt`, `llms-full.txt`), error catalog, copy-pasteable examples.
- `humans/` — thin human-shaped surface (landing, narrative concept walk, dashboard guide).
- `glossary.md` — generated from `concepts/`. Do not hand-edit.
- `vocabulary.md` — deprecated terms, layered-sense disambiguation.
- `licensing.md` — repo licensing notice.

## Working / internal surface

- `internal/` — working engineering reference. **Unmaintained.** Not cited by the public surface.
- `specs/`, `plans/`, `history/`, `future-work/` — pipeline artifacts (specs, implementation plans, archived design docs). Ephemeral.
- `examples/` — narrative case-making material; not yet promoted to the public surface.

The public surface is fully self-contained: it cites within itself and into `protocols/proto/v1/*.proto` (the public wire contract). It does not cite, link to, or reference any file under `internal/`, `specs/`, `plans/`, `history/`, `future-work/`, or `examples/`.
