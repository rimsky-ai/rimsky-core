---
audit: script-friendly-outcome
artifact: story:script-friendly-outcome
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:59:54Z
checked: 3
unaccounted: 0
---

# The three outcome classes a surrounding script branches on

Supported across all three classes the story names. Each was provoked
deliberately and read the way a script would: a manifest whose checks all pass, a
manifest mixing a passing and a failing check, and a manifest whose node waits on
a deliberately slow local server under a bound short enough to expire. The
branching was a real shell `case` on the exit status with the transcript
discarded outright, so nothing could have been settled by reading output. The
three classes came back distinct and in the promised order — all succeeded,
something failed, bounded out — with no two sharing a status, which is what makes
them branchable at all. Three checks, none failing.
