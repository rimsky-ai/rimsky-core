---
audit: design-link-annotations
artifact: decision:design-link-annotations
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:44:16Z
---

# Code cites design artifacts by slug, not the reverse

Supported. The codebase carries slug-keyed annotations at enforcement sites
in exactly the direction the decision describes — code pointing at
`.ok-planner/design/{concepts,stories,decisions}/<slug>.md` — never a
design doc citing a code path. A repo-wide grep of `.go` files found
`@concept:` in 467 files, `@story:` in 135 files, and `@decision:` in 220
files, each written as `// @<tag>: <slug>` immediately above the function,
struct, or block enforcing that artifact's commitment (spot-checked in
`cmd/rimsky/cli/compose/run.go` and `cmd/rimsky/cli/compose/artifact.go`,
each carrying several). `.ok-plumbline/config.json` registers all three tags
against their `.ok-planner/design/<kind>/{slug}.md` file templates, and the
citation-resolution lint (run via the vendored `plumbline` binary, clean
against the current tree) fails any annotation whose slug does not resolve
— the mechanical guarantee that the link stays live.
