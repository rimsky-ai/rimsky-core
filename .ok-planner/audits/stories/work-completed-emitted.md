---
audit: work-completed-emitted
artifact: story:work-completed-emitted
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:48:35Z
---

# Starts and completions join in the ledger, so durations and finish audits compute

Supported. Driven through the public surface against a released-image stack
paired with a third-party executor rebuilt for Linux by the run, on one instance
whose six dispatches take six dispositions — success, error, error-then-retry, a
park that resumes and succeeds, a park left outstanding, and a built-in
executor's dispatch. Twelve checks, none failing. The ledger carried seven starts
and five completions; joined on dispatch id, all five dispatches that reached a
terminal carried a completion, no completion named a dispatch that never started,
and each completion named its terminal kind, with success and failure
distinguishable. The two unpaired starts are the product's own answer to a
"did-everything-finish" audit rather than a gap: the parked-then-resumed dispatch
started twice and completed once, and the single start with no completion was
exactly the dispatch the park roster still held, on the node the template told to
park. Durations came from the two timestamps alone, non-negative for all five
completed dispatches.
