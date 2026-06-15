---
decision: doc-residue-reshape-pass
status: as-is
---

# Doc-residue cluster reshape pass

## Choice

The doc-residue comment-hygiene cluster gets a dedicated pass with a reshape-first per-site rule. When the comment sits directly above a package-level declaration (Go: `func` / `type` / `const` / `var`; TS/JS: `export function` / `export class` / `export const` / etc.), the comment is reshaped so its first word names the declaration on the next non-comment line and the body describes what the symbol IS, satisfying Plumbline's GoDoc / JSDoc exemption. When the comment is not in a doc-position (above an inside-function declaration, a divider that the cluster heuristic surfaced here, or otherwise), the comment is resolved per `decision:comment-hygiene-uniform-rule`.

## Rationale

The doc-residue cluster is bimodal: roughly half its sites are package-level declaration docs where GoDoc / JSDoc is the conventional shape agents reading the code expect, and the other half are inside-function why-comments where the doc convention doesn't apply. A dedicated pass with reshape evaluated first nudges the package-level half toward the conventional shape without forcing the executor to invent that priority during a tag-or-delete-framed pass.

## Alternatives

Treating doc-residue uniformly under `decision:comment-hygiene-uniform-rule` — rejected because the GoDoc-position half of the cluster genuinely benefits from the conventional shape, and a tag-or-delete framing under-serves that.
