---
audit: anonymous-mode-bootstrap
artifact: story:anonymous-mode-bootstrap
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095811-enroll-route-rejects-anonymous-identity
---

# Fresh deployment opens then locks down

Unsupported. The bootstrap-then-lockdown arc the story describes is solid and directly end-to-end tested: an unauthenticated request is admitted before any key exists, and refused the moment the first key is minted. But one action is code-verified to reject the anonymous identity outright regardless of anonymous mode's wildcard grant — service enrollment, reachable whenever mutual-TLS peer authentication is configured, independent of whether any api-key has been minted. Checked across every identity-conditional handler in the control API, this is the sole such case.

## Referrals

- referral: whether "action" in the story is meant to cover machine-to-machine service enrollment (a distinct, orthogonal peer-auth concern) or only ordinary operator control-plane actions
  established: the enrollment gap is real and reproducible in code; whether it falls inside the story's intended scope is a design-scope judgment, not a code question
  discipline: human-review
