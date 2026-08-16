---
audit: clean-lint
artifact: story:clean-lint
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:25:00Z
---

# The whole tree passes the project's lint, and both checks are demonstrably live

Supported. Seven checks across three legs. The repository carries the lint
binary a maintainer runs, its configuration switches off no check, and it
declares five citation tags. Run over the repository root under that same
configuration, the lint exited clean with no output. Both checks were then shown
to fire rather than sit inert: a throwaway tree carrying a stray comment was
reported by the comment check, a tree carrying an unresolvable citation was
reported by the citation check, and a third tree carrying a citation that does
resolve was accepted, so the citation check discriminates rather than always
failing.

## Compliance

The body names the specific tool the project lints with, which is an implementation choice recorded in a decision; compliant text says the codebase passes the project's lint with every check active.
The benefit clause serves a design artifact's accuracy rather than a user need — "so that `decision:coding-style` accurately describes the active configuration"; compliant text says what the maintainer gets, e.g. "so that every contributor's change is held to the same standard and I can tell at a glance whether the tree is clean".
