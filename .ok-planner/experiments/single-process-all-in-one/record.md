---
experiment: single-process-all-in-one
commit: d977250c
---

# One process for three roles, and the blob backend that needs it

The story makes two claims — one process serves all three roles, and the memory
blob backend works there because they share it — so this directory holds two
runnable ways.

## way-one-process.py

### What it ran against

A `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` with its baked zero-config
SQLite defaults. The script reads the container's process table and drives a
one-node template through the control API.

### What was observed

The container's process table held exactly one rimsky process, the multi-role
entrypoint itself, with no per-role child beside it; the entrypoint's log
reported it had taken all three roles. All three were serving out of that one
process: the control API answered its probe, one supervisor was registered, and a
node dispatched and settled fresh. The process count was still one after the
roles had done that work.

Eight checks, none failing.

## way-memory-blob.py

### What it ran against

The same image with a mounted config selecting the memory blob backend and a
256-byte spill threshold, running a template whose node carries an 8700-byte
attribute payload. The script then starts a `rimsky` container with the same
config and a command naming `rimsky-control-api`.

### What was observed

The all-in-one deployment accepted the memory blob backend, ran the node to
`terminal/success`, and read the whole 8700-byte payload back through the control
API — the payload the supervisor role spilled, read by the control-api role out
of the same in-process map.

The same config in a single-role container was refused at startup with a non-zero
exit, and the refusal named the memory backend as dev-only and named the
single-process mode it requires.

Five checks, none failing.

RESULT: PASS
