---
issue: substitution-documentation-absent
kind: audit
category: conflicting
artifacts:
  - story:substitution-doc-accuracy
status: answered
opened: 2026-08-16T08:55:54Z
---

# The story promises a substitution document this repository does not carry

Does the repository carry a substitution document whose listed source kinds a template author can trust to match what the resolver accepts, as `story:substitution-doc-accuracy` promises?

Yes. `decision:doc-accuracy-gates` names this exact artifact: "the substitution-doc gate (the documented source-kind list must match the runtime resolver's dispatch set)." That gate exists in the tree today, not only as a release artifact: the package doc comment atop `lib/graph/attribute/substitution.go` lists the six source kinds (claim, params, nodes, messages, child, env), and `TestSubstitutionDocMatchesResolverDispatchSet` in the same package — annotated `@story: substitution-doc-accuracy` and `@decision: doc-accuracy-gates` — fails the build the moment that list and the resolver's dispatch switch (`resolveDirectiveValueRaw`) diverge. The filed Problem's premise, that "there is no such documentation to read," holds only if "documentation" is narrowed to the release documentation corpus or a `docs/` folder; it does not account for the GoDoc-form documentation `decision:doc-accuracy-gates` already names and gates, which is exactly the substitution documentation the story asks a template author to trust.
