---
issue: stories-name-rimsky-yml-and-config-keys
kind: audit
category: muddy-boundary
artifacts:
  - story:portable-template-across-modes
  - story:local-orchestrator-zero-config
  - story:claude-agent-mcp-servers-per-node
  - story:claude-agent-expose-env-per-node
status: verified
opened: 2026-07-24T00:00:00Z
---

# User-promise documents are quoting config keys — and the pattern is spreading

Rimsky's "story" documents each record a durable user promise, and their governing rule is strict: a story owns the *need* ("operators can bound which tool servers an agent may reach"), never the *mechanism* (which YAML file, which field name). Four stories violate this by naming the main config file (`rimsky.yml`) and specific field paths in their prose — two of them quoting exact config keys (`cli.mcp_servers`, `cli.expose_env`, for a coding-agent executor's per-node tool-server and environment-variable controls) as the core of what they describe. The point of the rule is durability: field paths get renamed freely pre-v1, and every rename silently falsifies whichever story quoted it.

The four aren't equally guilty, and the problem is bigger than four. The two config-key stories clearly prescribe mechanism; the other two only say "no config file needed" — arguably a legitimate user-observable fact. Meanwhile the same pattern recurs in several stories outside this issue's scope (the coding-agent story, service enrollment, mutual-TLS), so a four-file fix just relocates the drift. One practical snag: no decision documents exist yet for the two coding-agent config keys, so stripping those stories without authoring replacements would leave that detail homeless in the corpus. One of them also still describes the executor in terms of an implementation language it was ported away from — bonus rot to clean in the same pass.

## Options

- **Corpus-wide sweep**: strip mechanism from every story that carries it, cite the config concept and decisions instead, and author the two missing decisions where content needs a home.
- **Fix only the two clear offenders**, reading absence-mentions ("no config needed") as legitimate observables.
- **All four, but no wider** — resolves the filing as scoped, leaves known siblings violating the same rule.

The ruling decides scope (four files or the catalog), severity treatment (are absence-mentions violations?), and whether the two new decisions get authored.

## Ruling

> Recommended ruling (/recommend-rulings): Expand to a corpus-wide
> stories sweep (the pattern recurs beyond the four flagged files):
> strip config field paths and mechanism from story bodies, citing
> concepts/decisions instead, and author the two missing claude-agent
> per-node decisions where stripped content currently has no home.
>
> Rationale: The story rule is unambiguous — stories own the need,
> decisions own the how — and fixing four files while siblings carry
> the same violation just moves the drift. Same reading as the
> decisions-enumeration ruling.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
