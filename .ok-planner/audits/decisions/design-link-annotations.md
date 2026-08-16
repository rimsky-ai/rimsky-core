---
audit: design-link-annotations
artifact: decision:design-link-annotations
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
---

# Whether code cites design artifacts by slug at the enforcement sites, and whether those citations resolve

Supported, and the surface is large. Source files carry 2,311 citations across 673 Go files — 1,474 concept, 607 decision, and 230 story — and every one resolves: taking the distinct slugs cited and checking each against the corpus finds a file for all 75 concept slugs, all 85 story slugs, and all 222 decision slugs, with no dangling or slug-less tag anywhere (the single tag-shaped line without a slug is a pattern literal inside a test). The direction is the one the choice picks: the link lives in code keyed by slug, and the citation-resolution check runs on every edit through a blocking hook and over the whole tree in the suite, so a renamed artifact breaks loudly instead of silently orphaning a pointer.
