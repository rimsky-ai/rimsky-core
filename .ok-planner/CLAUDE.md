# .ok-planner — workflow folder, not project documentation

This directory holds workflow scratch produced by the ok-planner
skills: specs, plans, sketches, implementation notes, and archived
versions of the same. It is **not** living documentation of the
codebase.

## Default behavior for agents: ignore this folder

Unless the user or an active skill (e.g. `/brainstorm`,
`/write-plan`, `/execute-plan`, `/review-plan`, `/sketch`)
explicitly directs you here, ignore `.ok-planner/` and its contents:

- **Do not consult these files to understand the project.** They
  reflect what someone was thinking at a moment in time. The
  codebase is the source of truth; these artifacts are not.
- **Do not include `.ok-planner/` files in general repository
  exploration**, codebase walkthroughs, or "how does this project
  work" research. Skip them the same way you would skip a build
  directory.
- **Do not propose updating, refreshing, or reconciling these files
  with the current state of the code.** Drift between an old plan
  and the current code is expected and fine. The artifact stays as
  it was written.
- **Do not edit, rename, move, or delete files here on your own
  initiative**, even if they look stale, redundant, or wrong.

## When it is OK to touch this folder

- The user explicitly asks (e.g. "update the spec at
  .ok-planner/specs/foo.md", "what did we decide about X — check
  the old plan").
- An ok-planner skill is running and directs you to read or write
  specific files here as part of its documented process.

In those cases, do exactly what the user or skill asked, then stop.
Do not expand the scope to "while I'm in here, I'll also fix..."

## Layout

- `specs/` — active specs from `/brainstorm`
- `plans/` — active plans from `/write-plan`, plus their
  `-notes.md` implementation notes written during execution
- `sketches/` — design sketches from `/sketch`
- `history/specs/` and `history/plans/` — specs and plans archived
  here automatically when an execute-* skill finishes a plan
