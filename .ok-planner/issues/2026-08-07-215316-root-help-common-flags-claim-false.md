---
issue: root-help-common-flags-claim-false
kind: human
category: cli
artifacts:
  - decision:cli-verb
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:53:16Z
github: https://github.com/rimsky-ai/rimsky-core/issues/93
---

# Root help promises four flags on "all verbs"; five command families reject them

The CLI's root help prints a block headed "Common flags (all verbs)" listing four
flags. Five families of verbs don't accept them, and passing one to any of those
fails outright with `flag provided but not defined` — not a warning, a parse
error before the command runs.

The same block also omits the two flags it most needs to list. The shared
flag-registration helper installs six flags, and the two it doesn't advertise
are `--key`, the only one that authenticates a request, and `--output`, the long
form of a flag the block does list in its short form.

## Which verbs actually get them

Verified against the current tree. The shared helper is reached from most
instance, template, asset, node, message, parked, lineage, admin and health
verbs — through a common wrapper — plus `run`, `ctx list`, and three of the
compose subcommands. It is not reached by any of: the eight `auth`
subcommands, the three `agent` subcommands, the eight `conformance`
subcommands, `compose run`, or `ctx use | add | rm | current`. Each of those
builds its own flag set. (The filed issue's file citations have drifted — the
registration now happens through a shared wrapper rather than at the call sites
it named — but the set of verbs on each side is unchanged.)

There is a second trap inside the same block. A `conformance` subcommand also
takes an `--endpoint` flag, but it means the service under test, not the control
API. Same spelling, different target.

## Ruling

> Generated ruling (/verify-issues): retitle the block to name what it actually
> covers — the control-API verbs, not all verbs — list the two flags it omits,
> and add a line naming the verb families that parse their own flags instead.
> Help text that enumerates a surface must match the surface; a printed claim
> the binary contradicts on the first try has exactly one compliant correction,
> and the verb families were re-verified one by one against the current tree.
> Verified against the tree as it stands; nothing was applied.
