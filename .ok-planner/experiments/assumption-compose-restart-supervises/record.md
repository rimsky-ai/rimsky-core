---
experiment: assumption-compose-restart-supervises
commit: PENDING
---

# What `instances[].restart` supervises

## What it ran against

One `rimsky-all-in-one` container from this tree's image set and two compose
projects, one declaring `restart: always` and one `restart: never` as the
control. Each is brought up, its instance is force-terminated, and the run
then polls the instance listing for a re-creation without running anything —
if a compose runtime supervises the instance, it has an idle deployment and a
terminal instance to act on. Afterwards the run invokes `compose plan` and
`compose up` by hand to establish what the policy does do.

## What was observed

Nothing supervises. Under `restart: always` the instance stayed terminated
across 30 polls, live instances went from 1 to 0 and stayed there, and
`compose status` kept reporting the instance as `in-manifest` with no sign it
had died. The control behaved the same way.

The policy is real but is only read by the next hand-run `compose up`. At that
point `compose plan` under `always` showed two changes — `instance-delete
compose:restart-always:one (restart=always)` then `create
compose:restart-always:one` — and applying them brought the count back to 1.
Under `never` the same plan said `no changes`, leaving the terminal instance
in place. So `restart` classifies what a future apply does with an instance
that has already terminated; it is not a restart policy in the container
sense, and between applies nothing is watching. 2 checks, 0 pass, 2 fail.
