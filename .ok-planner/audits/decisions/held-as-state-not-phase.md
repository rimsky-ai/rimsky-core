---
audit: held-as-state-not-phase
artifact: decision:held-as-state-not-phase
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:48:37Z
---

# Held as a first-class node-run state with cascade deferred to auto-terminal resolution

Supported. The node-run state machine declares seven states with held among them, and its transition table gives held its own row: a running run reaches it by the handler-held reason and leaves it only by auto-terminal commit, auto-terminal abandon, instance kill, or sibling cancellation — commit settling it to the success state and abandon to the failure state, exactly as the decision says. Held is a member of the in-flight set both in the exported list and in the predicate, so the gate that seals in-flight senders against a receiver dispatching treats it like the other four, with one deliberate carve-out for holding-subgraph co-members that is the read side of the same deferral. The transition happens on both outcome polarities: the success terminal and the error policy each probe for active held claims before settling and, when any exist, transition to held under the same reason rather than settling. It also covers acquirers and co-holders uniformly — the probe unions the claim handles the run acquired with the claim-holder rows naming it as a co-holder, and the deferred-cascade fire iterates every co-holder of the resolving handle plus the acquirer. The deferral itself is a receiver filter, not a suppression: at the running-to-held transition the run cascades its terminal signal to holding-subgraph members only, and when the portfolio finally resolves, each holder independently emits its own signal to non-members and drains its wait set. All-committed yields a success signal carrying the holder's settlement attributes; any abandoned yields the abandoned error class. Scenario coverage is broad — more than a dozen suites drive held paths, including the co-member gate skip, held acquirer pass and block, claim handoff, and the fan-out and delegation compositions.
