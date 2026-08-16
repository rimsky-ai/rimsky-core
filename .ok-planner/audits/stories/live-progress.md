---
audit: live-progress
artifact: story:live-progress
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:45:38Z
---

# An operator watching a one-shot run sees each node settle while the run continues

Supported. Driven through the public surface with the CLI built from this tree,
on a two-instance one-shot run where one instance settles at once and the other
waits on an upstream that sleeps eight seconds; every progress line was stamped
with the second it reached the terminal. Four checks, none failing. Both
instances emitted per-node lifecycle lines, and the fast instance's node outcome
was on screen one second in — seven seconds before the slow upstream could even
answer — with its instance summary at two seconds, nine seconds before the
command returned. The slow instance's node and summary arrived at eleven
seconds. A watcher therefore sees at any moment which work has settled and which
is still outstanding, which is the separation between healthy work and a hang
the story rests on.
