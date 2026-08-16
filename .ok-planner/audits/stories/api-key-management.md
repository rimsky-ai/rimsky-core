---
audit: api-key-management
artifact: story:api-key-management
text: noncompliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:39:20Z
---

# An operator runs the whole api-key lifecycle through the auth verbs

Supported. All seven capabilities the story names were driven against a fresh
all-in-one deployment through the seven public auth verbs, with each effect
confirmed independently against the control API using the key in question.
Status on the untouched deployment reported anonymous with zero keys;
bootstrapping minted the admin key, printed its plaintext once, moved status to
authenticated with one key and one admin, and a second bootstrap attempt exited
non-zero. Minting with a read-only role produced a key that read instances (200)
and could not register a template (403), so the role bound; an expiring key
worked. Listing named all three keys, carried no field matching "plaintext", and
neither listing nor inspection reproduced the live plaintext of the key being
inspected, while inspection reported the key's name and its grant. Revoking made
the key's next request 401, dropped it from the default listing, and kept it
visible when revoked keys were requested. Rotating printed a new plaintext and
the old key's revoke time; the new key worked immediately, the old key kept
working inside the grace window and stopped being accepted once it closed, with
the new key still answering.

## Compliance

The "so that" clause restates the activity rather than naming a need — "so that I administer credentials end-to-end" is the listed capabilities summed; compliant text would name what the operator gets from the lifecycle, e.g. "so that a credential I hand out can be scoped, replaced, or withdrawn without taking the deployment down."
