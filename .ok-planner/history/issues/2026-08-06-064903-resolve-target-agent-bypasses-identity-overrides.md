---
issue: resolve-target-agent-bypasses-identity-overrides
kind: audit
category: bug
artifacts:
  - concept:host-agent
status: promoted
opened: 2026-08-06T06:49:03Z
sprint: 2026-08-06-ruled-intake-drain.md
---

# The CLI's default-agent guess ignores the identity-file override the daemon honors

When an operator creates an instance without naming a target agent,
the CLI guesses one by reading the local host agent's identity file —
the small file where an agent persists who it is. The agent daemon
itself honors two documented ways to relocate that file
(`RIMSKY_AGENT_IDENTITY_FILE` and `--identity-file`,
`lib/runtime/hostagent/run.go:111-131`), but the CLI's guess does not:
`ResolveTargetAgent` (`cmd/rimsky/cli/instances.go:23-39`) reads — and
will silently create — the file at the default path only. An operator
who moved the identity file and runs `instance create` or `rimsky run`
without `--agent` therefore gets an instance targeted at a different
identity than the daemon actually routes under: dispatch silently
misroutes, and a stray identity file may appear at the default
location. Four call sites share the guess (`instance create`,
`rimsky run`, and both compose paths), none passing an override.

The corpus documents the daemon's identity persistence
(`concept:host-agent`) but is silent on the CLI guess needing to
consult the same override. Two pieces are separable: honoring the env
var in the guess is forced by parity with the daemon's own precedence;
whether the four commands also each grow an `--identity-file` flag is
a genuine surface question — `--agent <label>` already exists as the
explicit per-invocation override and bypasses the file entirely.

## Options

- Make the guess consult `RIMSKY_AGENT_IDENTITY_FILE` before falling
  back to the default path. Cost: none — it matches the daemon's
  existing precedence and adds no surface.
- Additionally add `--identity-file` to all four commands. Cost: new
  CLI surface across four call sites duplicating what `--agent`
  already provides explicitly.

The ruling decides whether env-var parity alone closes this or the
flag spreads too.

## Ruling

> Recommended ruling (/verify-issues): make the CLI's target-agent
> guess honor `RIMSKY_AGENT_IDENTITY_FILE`, and stop there — no new
> `--identity-file` flags on the four commands.
>
> Rationale: the env-var half is forced (the daemon already defines
> the precedence; the guess just fails to follow it), while the flag
> half duplicates `--agent`, which is already the explicit way to
> name a target per invocation. The flip case: operators routinely
> juggling multiple identity files on one machine would justify the
> flag; nothing observed suggests that pattern exists.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
