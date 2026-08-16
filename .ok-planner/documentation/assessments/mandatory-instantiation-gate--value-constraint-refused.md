---
assessment: mandatory-instantiation-gate--value-constraint-refused
subject: story:mandatory-instantiation-gate
way: value-constraint-refused
release: d977250c
outcome: held
warrant: experiment:mandatory-instantiation-gate
---
# Instance create refuses a value that breaks a referenced service's constraint

The audit drove `catalog:http-routes/POST /v1/instances` against an all-in-one deployment (`catalog:images/rimsky-all-in-one`), with each template's attribute configuration placed where registration's own composition check cannot see it, so the create-time gate was the only thing that could catch the misconfiguration. A value constraint imposed by the referenced service's own schema — a minimum item count on a collection left defaulted empty — was caught: the create was refused, `catalog:http-routes/GET /v1/instances` showed no instance afterwards, and the refusal body named the offending node, the offending attribute and the violated constraint rather than reporting a bare shape mismatch. A control template that satisfies both referenced services' schemas created cleanly with both its nodes, so the gate refuses bad configuration without refusing good configuration.

## Unverified remainder

The refusal is clear at the control API but thinner at the CLI: driving the same create through `catalog:cli-verbs/rimsky instance create` in the same run relayed only the summary line of the refusal and dropped the node, attribute and constraint detail. Whether that summary is enough for an operator to act on is a question for the product's own interface discipline, not something this run settles. One kind of value constraint — a minimum item count — was exercised; the way does not enumerate every constraint a service schema can impose.
