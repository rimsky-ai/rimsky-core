---
issue: claude-agent-declared-tags-inert
kind: audit
category: config-surface
artifacts:
  - concept:terminal-tag
  - concept:executor
status: promoted
opened: 2026-08-06T06:49:15Z
sprint: 2026-08-06-ruled-intake-drain.md
---

# claude-agent advertises a terminal-tag vocabulary nothing can ever emit

The bundled claude-agent executor can be configured to advertise a
vocabulary of terminal tags — labels a finishing run attaches so
downstream subscriptions can filter on them. The advertisement works
(`RIMSKY_EXECUTOR_DECLARED_TAGS`,
`lib/services/executors/claude-agent/schema.go:44`, surfaced through
the observability capabilities response), but nothing in the executor
can ever attach a tag: the agent-facing report tools accept no tags
parameter, the outcome struct has no tags field, and the callback
bodies are built without one (`agentrun.go:31`, `requestparse.go:23`).
The runtime's tag gate only rejects undeclared tags on settling
verdicts — it never produces any. So an operator who declares tags and
writes a subscription filtering on them gets a template that registers
cleanly and silently never fires.

The corpus defines the tag mechanism generically
(`decision:terminal-tags`, `concept:terminal-tag`) and says nothing
about which executors must emit; it is silent on this executor. But
claude-agent's own sibling mechanism sets the pattern: its error-class
vocabulary is advertised in full and every advertised class has a real
emission site. The tags knob breaks that discipline — and the bundled
http-node executor shows the honest version, genuinely writing tags
onto its outcomes (`lib/services/executors/http-node/server.go:303`).

## Options

- Thread tags through the report tools and callback body so the agent
  can set them. Cost: a new agent-facing parameter and a story for
  why an agent chooses tags — feature work nothing currently asks for.
- Remove the knob from claude-agent until something emits tags. Cost:
  a future story must reintroduce both ends together.
- Document the variable as reserved. Cost: a knob that silently does
  nothing — the exact trap the project's dead-surface discipline
  forbids.

The ruling decides whether claude-agent grows real tag emission or
sheds the inert advertisement.

## Ruling

> Recommended ruling (/verify-issues): remove it — drop
> `RIMSKY_EXECUTOR_DECLARED_TAGS` and the declared-tags plumbing from
> claude-agent, and reintroduce advertisement and emission together
> if a story ever wants agent-settable tags.
>
> Rationale: it matches the executor's own error-class discipline
> (advertise only what is emitted) and the project's dead-surface
> rule; the emission option is feature work with no driving story
> behind it. The flip case: a concrete near-term story for
> agent-chosen tags flips this to wiring emission through the report
> tools instead — the removal forecloses nothing.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
