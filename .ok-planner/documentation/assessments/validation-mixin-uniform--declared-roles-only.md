---
assessment: validation-mixin-uniform--declared-roles-only
subject: story:validation-mixin-uniform
way: declared-roles-only
release: d977250c
outcome: held
warrant: experiment:validation-mixin-uniform
---
# The roles the service declares are the roles it is called for

The run included a second executor peer that advertised the validation mix-in but declared a role it does not play in the template. It was never called, even though its node sat in the same template as the peer that was. The author's declared roles are therefore what is honoured — advertising the mix-in does not mean being asked about everything, and a service is not consulted about work it has nothing to do with.

## Unverified remainder

One mismatched declaration was exercised. The demonstration does not establish what happens when a service declares several roles at once.
