---
trap: env-unknown-vars-rejected
release: d977250c
---
# Evidence set — a misspelled `RIMSKY_*` variable is caught at startup and named, the same way a misspelled YAML key is.

Source of the prior: published-concept — `concept:rimsky-yml` ("Strict YAML decoding … any unknown key … fails at load with the offending key named")

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-env-unknown-vars-rejected)

# A misspelled variable against a misspelled key

## What it ran against

Four `rimsky-all-in-one` containers from the tree's own image tag. The first
carries five near-miss misspellings of variables the product does read
(`RIMSKY_CONTROL_API_PROT`, `RIMSKY_SCHEDULR_TICK_MS`, `RIMSKY_LOG_LEVE`,
`RIMSKY_METRICS_PORT_SCHEDULR`, `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOSTT`).
The second sets the same control-API port variable spelled correctly, so the
misspelling is shown to be a real miss rather than a setting that does nothing.
The third mounts a `/etc/rimsky/rimsky.yml` whose first key is `persistance`.
The fourth gives a variable the product does read an unusable value
(`RIMSKY_ENTRYPOINT_MIGRATE=yes`).

## What was observed

Eight checks, none failing.

The five misspellings changed nothing and were never mentioned. The container
came up and answered `/v1/health` on the ordinary port, so
`RIMSKY_CONTROL_API_PROT=8099` did not move the listener, and the whole startup
log named none of the five. Spelled `RIMSKY_CONTROL_API_PORT`, the same value
did move the control API, which the probe confirmed by reaching `/v1/health`
there.

The same typo made in YAML behaved the opposite way: the container exited
non-zero before serving, and the error named `persistance` as the offending
field. An unusable value on a variable the product reads also stopped the
container, with the message naming both `RIMSKY_ENTRYPOINT_MIGRATE` and the
rejected `"yes"`.

The boundary the runs draw: the product checks the values of the variables it
knows, and never checks the names of the ones it does not.

Runnables: `src:.ok-planner/experiments/assumption-env-unknown-vars-rejected/` at the stamped commit.
