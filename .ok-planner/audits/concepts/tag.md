---
audit: tag
artifact: concept:tag
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:08:13Z
---

# The movable template alias and its five invariants

Unsupported: the reserved-prefix rule claims enforcement on every path that can attach a tag to a template, and one of the three such paths does not enforce it. The other four invariants hold. Tags are persisted as a name-to-hash row that is freely repointed while the hash side is content-addressed and immutable, and instances bind to a hash resolved at creation, so moving a tag leaves live instances where they are — exercised end to end by a tag-management scenario test. The tag identifier pattern admits letters, digits, and a fixed punctuation set that excludes the path separator, so every legal tag stays addressable as one path segment. Repointing an existing tag at a different hash requires a tag-set grant scoped to that tag on both the routes that can do it, including the registration side effect, which returns forbidden with an explicit message when only registration permission is held. The gap is the reserved prefix: enumerating the server-side writes that attach a tag to a template gives three — tag creation, template registration, and the dedicated tag-move route — and the first two reject a reserved-prefix name from a non-privileged origin while the move route checks only the scoped grant, so a caller holding tag-set on an existing reserved-prefix tag can point it at any template without originating from the privileged path.
