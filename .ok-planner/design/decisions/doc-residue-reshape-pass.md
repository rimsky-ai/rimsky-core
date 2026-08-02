---
decision: doc-residue-reshape-pass
---

# Doc-residue comments resolve reshape-first

## Choice

Doc-residue comment-hygiene violations resolve under a reshape-first per-site rule rather than plain tag-or-delete. When the comment sits directly above a package-level declaration in a file that already carries the docstring opt-in marker, the comment is reshaped so its first word names the declaration on the next non-comment line and the body describes what the symbol IS, satisfying Plumbline's GoDoc / JSDoc exemption. When the comment is not in a doc-position, or the file lacks the opt-in marker, it resolves per `decision:comment-hygiene-uniform-rule` instead — reshape alone does not satisfy the exemption without the marker.

## Rationale

The doc-residue cluster is bimodal: roughly half its sites are package-level declaration docs where GoDoc / JSDoc is the conventional shape agents reading the code expect, and the other half are inside-function why-comments where the doc convention doesn't apply. Evaluating reshape first nudges the package-level half toward the conventional shape rather than leaving that priority to per-run judgment under a tag-or-delete framing. Gating reshape on the file's opt-in marker keeps the rule consistent with the marker being the sole authority for whether documentation comments are allowed in a given file — reshaping into GoDoc/JSDoc shape in a marker-less file would still fail comment hygiene.

## Alternatives

- Treating doc-residue uniformly under `decision:comment-hygiene-uniform-rule` — rejected: the GoDoc-position half of the cluster genuinely benefits from the conventional shape, and a tag-or-delete framing under-serves that.
