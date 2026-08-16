---
assumption: env-unknown-vars-rejected
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# a misspelled `RIMSKY_*` variable is caught at startup and named, the same way a misspelled YAML key is.

As operator who typoed a variable name, I would take it that a misspelled `RIMSKY_*` variable is caught at startup and named, the same way a misspelled YAML key is.

## Source

published-concept — `concept:rimsky-yml` ("Strict YAML decoding … any unknown key … fails at load with the offending key named")

## What a run would observe

boot a role with `RIMSKY_SCHEDULR_TICK_MS` set and see whether startup complains or silently ignores it.

## Measured

Experiment `assumption-env-unknown-vars-rejected` (eight checks, none failing)
drove four `rimsky-all-in-one` containers at this tree's image tag. The prior
does not hold. A container carrying five near-miss misspellings of variables
the product does read — `RIMSKY_CONTROL_API_PROT`, `RIMSKY_SCHEDULR_TICK_MS`,
`RIMSKY_LOG_LEVE`, `RIMSKY_METRICS_PORT_SCHEDULR`,
`RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOSTT` — came up and served, and the
startup log named none of them. The miss was real: spelled
`RIMSKY_CONTROL_API_PORT`, the same value moved the control API's listener,
which the probe reached. The same typo made in YAML behaved the opposite way:
a mounted `rimsky.yml` whose first key was `persistance` stopped the container
before it served, with the error naming the offending field; and an unusable
value on a variable the product does read (`RIMSKY_ENTRYPOINT_MIGRATE=yes`)
stopped it too, naming both variable and value. The product checks the values
of the variables it knows and never checks the names of the ones it does not,
so an operator's environment typo is a silently unapplied setting while the
same typo in YAML is a loud refusal.
