---
assessment: template-lifecycle--register
subject: story:template-lifecycle
way: register
release: d977250c
outcome: held
warrant: experiment:template-lifecycle
---
# Registering a workflow definition into the catalog

The audit registered a definition through `catalog:cli-verbs/rimsky template register` against a container of the released all-in-one image. Registration returned a content-addressed id, `catalog:cli-verbs/rimsky template list` showed the definition in the registered state, and `catalog:cli-verbs/rimsky template get` returned the stored spec unchanged. The operator therefore has a catalogue entry they can read back and refer to by id.

## Unverified remainder

One definition was registered. The demonstration does not establish what registering the same definition twice returns.
