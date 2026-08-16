---
audit: substitution-doc-accuracy
artifact: story:substitution-doc-accuracy
text: noncompliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:25:00Z
---

# There is no substitution documentation to read, though the one listing the product does expose is exact

Unsupported, because the artifact the story promises accuracy of cannot be
reached. The story's capability is a template author reading the substitution
documentation and trusting its list; at this tree the product ships no such
documentation — the repository carries no documentation sources, and the run's
public-surface extraction enumerates no documentation among its kinds — so no
run can demonstrate a listing matching or failing to match. What the run did
establish is the substance behind the promise, through the only listing of
source kinds the product exposes publicly: the template validator's refusal of
an unrecognised source, which enumerates six kinds. Each of those six was then
driven through to a dispatch against a running deployment and read back off the
node surface — a param, an upstream node's attribute, a typed message's field
and a process environment variable each arriving verbatim, a claim resolving in
both its payload and its scope form, and a partition key resolving inside a
fan-out work unit — and the set that resolves is exactly the set the refusal
lists, neither wider nor narrower. So a template author driving the product
silently misses no supported source; there is simply no document whose accuracy
this story could be about.

## Compliance

The body names an internal component — "what the resolver actually recognizes" — which belongs to a decision, not the story; compliant text says "the source kinds the product actually accepts".
