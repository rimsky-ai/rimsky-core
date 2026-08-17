---
issue: surface-intent-go-module-embedding-surface
kind: audit
category: unclear
artifacts:
  - concept:module-layout
status: verified
opened: 2026-08-16T04:07:09Z
---

# The surface intent does not say which Go modules a consumer may embed

The intent's general rule says a consumer embeds rimsky as a Go module. The workspace holds four modules: the root, the protocols library, the foundation library and the bundled services. The intent does not say which of the four a consumer may embed. The release notes name only the root module and the protocols module as fetch targets. The lint treats the foundation module as internal. The foundation module and the services module carry no tags and resolve only through workspace replacements. The extractor defaulted the two unnamed modules internal. The ruling amends the intent.

## Options

- Public: root and protocols. Internal: foundation and services. This matches the lint posture and the release notes; cost: none new.
- Every workspace module is public; cost: two untagged, replace-resolved modules become promises a consumer cannot fetch.

The ruling decides which modules carry an embedding promise.

## Ruling

> Recommended ruling (/verify-issues): Name the root module and the protocols module as the public embedding surface, and the foundation and services modules as internal.
>
> Rationale: the lint already isolates foundation as internal, the release notes already fetch only the two, and a consumer cannot fetch the other two today. Flip case: if the services module is meant to be imported so a consumer can host a bundled service in-process, it needs tags and a promise, and the intent should say so then.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
