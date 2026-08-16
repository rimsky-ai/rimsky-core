---
assessment: api-key-management--inspect
subject: story:api-key-management
way: inspect
release: d977250c
outcome: held
warrant: experiment:api-key-management
---
# Inspecting one key to see what it is allowed to do

`catalog:cli-verbs/rimsky auth show` reported the inspected key's name and the grant it carries, which is what an operator needs to decide whether a credential in circulation is still the right shape for its holder. Inspection is not a way back to the secret: the output did not contain the live plaintext of the key being inspected.

## Unverified remainder

None: the passing run demonstrates the way as promised.
