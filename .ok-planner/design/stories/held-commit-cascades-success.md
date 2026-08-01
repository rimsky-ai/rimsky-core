---
story: held-commit-cascades-success
status: as-is
---

# Held work cascades terminal/success only when auto-terminal commits

## Role

As a template author whose downstream node depends on data that an upstream held-claim work-unit produces, I can know that my downstream subscriber sees `terminal/success` from the upstream only when the held work has committed — not the moment the upstream returns its held terminal. Provisional held results never reach my subscriber.

## Capability

When a node-run's terminal includes a held=true claim, the run transitions `running → held` and the cascade walker does NOT fire downstream signals at that moment. The cascade walk is deferred until the auto-terminal handler resolves: on Commit, the run transitions `held → fresh` AND the cascade walk fires `terminal/success` downstream at that moment. Downstream subscribers see only committed results.

## Business value

Without cascade-defer-on-held, downstream nodes can act on provisional data that the held work later abandons — and there is no retract mechanism, so the downstream's effects persist after the rollback. The "held" state exists precisely to express "this work is provisional pending commit"; firing cascade on the held terminal collapses that semantic. With cascade-defer-on-held, downstream sees committed-or-nothing: either the work committed and the downstream cascades, or the work was abandoned and the abandoned-signal cascades instead.

