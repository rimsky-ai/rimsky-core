---
audit: client-context
artifact: story:client-context
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:07:00Z
---

# Several deployments registered, switched between, inspected, and removed

Supported, and shown to re-target rather than merely record. Two independent
deployments were booted and each seeded with a distinct template while still
addressed explicitly; after both were registered by name, no later command named
an endpoint. All four operations the story claims were driven: registering both,
listing them with their endpoints and the current one marked, reporting the
current one, switching, and removing one. The switch was settled by consequence
rather than by output — with no endpoint flag anywhere, the template listing
returned the first deployment's template and not the second's, and after the
switch returned the second's and not the first's — so the current context really
decides which deployment answers. Removal dropped the named entry and left the
other listed. The CLI ran against an empty home directory with every rimsky
environment variable unset, so nothing outside the registrations could have
supplied an endpoint. Sixteen checks, none failing.

## Compliance

- The body names the delivery surface ("in the `rimsky` CLI"), which the story
  rules place in decisions; the compliant text names the outcome only, e.g. "As an
  operator on a dev machine, I can register several deployments by name, switch
  which one my commands go to, and inspect or remove those registrations, so that
  I run commands against several deployments without repeating connection
  details."
