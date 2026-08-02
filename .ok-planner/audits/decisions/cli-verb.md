---
audit: cli-verb
artifact: decision:cli-verb
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:40:22Z
---

# One-shot verb sits as a sibling under the compose dispatcher

Supported. The compose command dispatcher routes exactly 5 subcommands — `up`, `down`, `plan`, `status`, `run` — through one switch statement; `run` operates on the same `rimsky-compose.yml` manifest format as the other four and, unlike them, does not require a reachable running rimsky (it self-hosts its own stack), matching the decision's stated asymmetry between the lifecycle verbs and the one-shot verb. A dispatch-level test confirms `run` is routed to its own handler rather than falling through the dispatcher's unknown-subcommand path.
