---
audit: portable-template-across-modes
artifact: story:portable-template-across-modes
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:07:00Z
---

# One template file, run unedited against both deployment modes

Supported: one file ran on both modes with nothing edited between them. The same
template was driven first against an all-in-one deployment on its baked
zero-config defaults, then against a genuinely split deployment — a postgres
container, a separate bundled-executor container, and three role containers whose
commands name the control-api, scheduler and supervisor roles against a shared
config. On each, the file registered, deployed, instantiated, reached a terminal
state, and left every node with one fresh run. Two things rule out a false pass:
the file's own hash was taken before and after and was unchanged, so no step
rewrote it for the second mode; and both deployments content-addressed it to the
same template hash, so each accepted the identical bytes rather than a
normalization of its own. Nine checks, none failing.
