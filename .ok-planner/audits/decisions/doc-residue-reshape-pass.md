---
audit: doc-residue-reshape-pass
artifact: decision:doc-residue-reshape-pass
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 2
unaccounted: 0
---

# Whether doc-position comments in opted-in files were reshaped and everything else went the uniform way

Supported. Exactly two source files in the tree carry the docstring opt-in marker, and both hold a package-level documentation block in doc position — the branch the reshape rule governs — and both satisfy the lint's documentation exemption, so nothing in that population was deleted where reshaping applied. The other branch holds too: across the remaining 1,637 Go files, no free-standing prose comment survives at all, which is what resolving by the uniform tag-or-delete rule produces. The dependency the choice states is real — the exemption is conditional on the marker, so reshaping an unmarked file's comment into documentation form would not have saved it.

## Remediation

- One of the two opted-in files opens its package block with a phrase rather than the declaration's name, which the lint accepts through its package-adjacency branch but which is not the first-word-names-the-declaration shape the choice describes.
