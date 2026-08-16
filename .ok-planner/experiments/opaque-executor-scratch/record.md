---
experiment: opaque-executor-scratch
commit: PENDING
---

# Executor-attached bytes surviving park, in-place retry and stale recovery

## What it runs against

`run.py` compiles a third-party executor from `probe-executor.go.txt` and runs
it on the host. That executor speaks the public executor protocol over gRPC:
`Executor.Execute` plus the `ExecutorObservability.Capabilities` handshake the
control API probes at template registration. `run.py` then boots a
`rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` with a mounted `rimsky.yml`
naming that executor at `host.docker.internal`, and drives it through the
control API.

The executor attaches three distinct byte strings containing non-UTF-8 bytes:
one to a Park outcome, one to an Error outcome, one to the Park that precedes a
stale recovery. On any dispatch that arrives carrying scratch, it reports the
SHA-256 digest and the length of what it received rather than the bytes
themselves. The template declares four nodes: one plain; one that parks; one
whose declared error class is routed to `retry` with a cap of one; and one that
parks, then returns an async-callback deferral it never answers under a
`max_quiet_period` of two seconds, so the runtime reaps the quiet dispatch and
re-dispatches the same node-run.

## What was observed

Ten checks, none failing. The plain node's dispatch carried no scratch. The
parking node emitted one `transient/park`, whose audit record carries only
`scratch_size: 36` and `scratch_spilled: false` and not the bytes. Its resume
was a second dispatch of the same node-run id and received back a byte string of
the same length whose digest matches the bytes attached to the Park. The
retrying node's recovery dispatch was likewise the same node-run id, was stamped
`PRIOR_RETRY_AFTER_ERROR`, and received back the digest and length of the bytes
attached to the Error. The quiet node's third dispatch was the same node-run id,
was stamped `PRIOR_STALE_RECOVERY`, and received back the digest and length of
the bytes its own first dispatch had attached to a Park two dispatches earlier.
Across the forty-six rimsky-authored records the run scanned, none carries any
of the three byte strings in any of its base64, hex or raw forms.
