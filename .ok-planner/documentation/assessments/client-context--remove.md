---
assessment: client-context--remove
subject: story:client-context
way: remove
release: d977250c
outcome: held
warrant: experiment:client-context
---
# Removing a registration without disturbing the others

`catalog:cli-verbs/rimsky ctx rm` dropped the named entry and left the other one listed and usable. Registrations are therefore independent: retiring a dev deployment from the CLI's list does not cost the operator the rest of their setup.

## Unverified remainder

The entry removed was not the current one; the run does not establish what the CLI does when the current context is the one removed.
