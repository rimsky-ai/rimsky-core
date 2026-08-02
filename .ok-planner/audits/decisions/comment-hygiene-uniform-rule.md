---
audit: comment-hygiene-uniform-rule
artifact: decision:comment-hygiene-uniform-rule
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:39:17Z
---

# One tag-or-delete rule applies per comment site, with no per-cluster carve-outs

Supported. The project's plumbline config (`.ok-plumbline/config.json`) declares exactly the three citation tags the rule names (`@concept:`, `@story:`, `@decision:`) and adds no per-cluster or per-category exemption logic; the referenced override, `decision:doc-residue-reshape-pass`, exists as a live decision. Running the vendored lint binary against the whole working tree now reports zero violations, consistent with the uniform per-site rule being the one actually applied rather than a bespoke set of cluster rules.
