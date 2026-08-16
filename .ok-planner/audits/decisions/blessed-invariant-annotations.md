---
audit: blessed-invariant-annotations
artifact: decision:blessed-invariant-annotations
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
---

# Whether safety properties live named in concept docs with no numbered catalog and no dedicated tag

Supported. Neither retired surface exists: no invariant catalog file exists in the design corpus, and no source file anywhere carries a dedicated invariant annotation — the only occurrence of the retired tag name in the tree is inside an archived release note. The replacement is in place. Code sites cite the owning concept by slug through the concept annotation, which is one of the three tags the lint resolves against the corpus, and a fitness test walks every Go file in the tree and fails on any numbered invariant reference, so the numbering cannot return to code. Tests carry the descriptive names in their file names and assertion messages, which is how coverage is meant to be read. One residue worth naming, which the choice does not speak to either way: 17 concept documents still append bare numbers to properties they otherwise state descriptively, and those numbers resolve to nothing now that the catalog is gone.
