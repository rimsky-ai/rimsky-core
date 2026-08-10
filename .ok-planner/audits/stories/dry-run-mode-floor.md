---
audit: dry-run-mode-floor
artifact: story:dry-run-mode-floor
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:25:00Z
---

# An api-key whose grant pins a write action to dry-run

Supported. Three keys minted through the public key-creation verb settled it: a
key granting one write action with dry-run mode returned the synthetic
would-have envelope for a request that carried no flag at all, and the write did
not land; the same key asking for a real write with the request flag set to
false got the same envelope and the same absence, so the holder cannot escalate
its own credential; a control key holding the same action unpinned performed the
real write; and a key holding both the pinned grant and a second grant covering
the same action executed for real, which is the proviso the story states. Both
escalation routes a holder has — omitting the flag and setting it false — were
exercised.
