---
audit: project-agnostic
artifact: decision:project-agnostic
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:21:13Z
---

# No consumer vocabulary appears in the shipped examples, fixtures, or docs

Supported by reading. Enumerated every YAML file in the tree outside the planner estate and the CI workflows — 22 of them, covering the two bundled claim-producer config examples, the stub producer's config example, the two configs baked into the all-in-one image, the eleven compose and template fixtures, and the three demo templates — plus the five demo shell scripts and the repository README. Every template, node type, executor name, instance key, and table name in that set is generic and illustrative: worker, stub, counter, verifier, tpl-a, inst-a, fast, slow, oops, items and issues tables, a review-queue pick policy. Nothing names or assumes a specific consumer, and the README states the domain-agnostic framing explicitly rather than assuming a workload. The only proper nouns are the project's own and the vendor's copyright and licence headers. No lint rule or fitness test enforces the stance; it is stated in the project's rules surface and held by the artifacts as they stand.
