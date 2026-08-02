---
decision: process-role-unified-message-covers-rimsky-run
---

# The unified-marker error text names all three setters

## Choice

The process-role env marker's blob-config error text — the message shown when the memory blob backend is configured outside single-process mode — names the three genuine deployment paths that set the marker: the entrypoint's no-command all-in-one path, the compose one-shot, and the ephemeral-run verb in self-host mode. A fourth setter, the conformance runner's in-memory-backend test path, is deliberately left unnamed — it satisfies the same gate but is not a deployment an operator debugging the error is in (see `decision:single-process-mode` for the marker's full setter list).

## Rationale

The message is the operator's first contact with the marker; naming only one setter reads as "the other paths don't count" and sends users debugging a mode they are legitimately in.

## Alternatives

- Leave the text naming only the entrypoint path — rejected: factually incomplete once two CLI verbs also set the marker.
