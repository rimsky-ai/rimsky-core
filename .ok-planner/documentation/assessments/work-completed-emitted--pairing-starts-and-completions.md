---
assessment: work-completed-emitted--pairing-starts-and-completions
subject: story:work-completed-emitted
way: pairing-starts-and-completions
release: d977250c
outcome: held
warrant: experiment:work-completed-emitted
---
# Every dispatch that finished says so in the ledger

The audit drove one instance whose six dispatches take six dispositions — success, error, error-then-retry, a park that resumes and succeeds, a park left outstanding, and a built-in executor's dispatch — and read the ledger through `catalog:http-routes/GET /v1/events`. It carried seven `catalog:event-kinds/work_started` events and five `catalog:event-kinds/work_completed` events. Joined on dispatch id, all five dispatches that reached a terminal carried a completion, no completion named a dispatch that never started, and each completion named its terminal kind with success and failure distinguishable. The two unpaired starts are the product's answer to a did-everything-finish audit rather than a gap: the parked-then-resumed dispatch started twice and completed once, and the single start with no completion was exactly the dispatch the park roster still held (`catalog:cli-verbs/rimsky parked`), on the node the template told to park.

## Unverified remainder

Six dispatches over one instance were exercised. The demonstration does not establish the pairing across a supervisor restart while dispatches are in flight.
