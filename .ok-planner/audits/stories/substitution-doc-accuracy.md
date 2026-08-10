---
audit: substitution-doc-accuracy
artifact: story:substitution-doc-accuracy
determination: unclear
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Whether the substitution documentation lists what the resolver recognises

Unclear. The story's subject is documentation a template author reads, and no
such document exists in this repository: the repository carries no documentation
sources, and the surface ruling's seven kinds — CLI verbs, HTTP routes, gRPC
RPCs, env vars, images, template keys, config keys — enumerate no documentation
element, so no user-vantage run can compare a listing against the resolver. The
one prose listing of source kinds this tree holds is a package comment on
internal source, which a template author has no public way to read. What was
measured instead: the only listing reachable through the public surface is the
message the template validator returns when it rejects an unrecognised source
kind, that message enumerates six kinds, and all six of them — claim, params,
nodes, messages, child, env — resolved at runtime in a driven dispatch, claim in
both its payload and claim_scope forms. That is a listing matching the resolver
exactly, but it is not the documentation the story promises.

## Compliance

- The capability clause names an internal component ("what the resolver actually
  recognizes") rather than something the user does; compliant text states the
  user-side outcome, such as being able to use any source kind the documentation
  lists and have it resolve at runtime.
