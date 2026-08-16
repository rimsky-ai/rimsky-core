---
audit: single-process-all-in-one
artifact: story:single-process-all-in-one
text: noncompliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:50:41Z
---

# All-in-one deployment serving all three roles from one process

Supported: the all-in-one image runs as a single process that carries all three
roles, and the shared-memory blob backend works there because of it. The running
container's process table held exactly one rimsky process — the multi-role
entrypoint, with no per-role child beside it — and stayed at one after the
deployment had done work; all three roles were serving out of it, evidenced by
the control API answering, one supervisor registered, and a node dispatched and
settled fresh. The blob half was measured by round-trip rather than by
configuration acceptance: with the memory backend selected and a 256-byte spill
threshold, a node carrying an 8700-byte payload ran to success and the whole
payload read back through the control API — written by the supervisor role, read
by the control-api role, out of one in-process map. The same configuration given
to a single-role container was refused at startup with an error naming the
backend as dev-only and the single-process mode it requires, so the coupling the
story rests on is enforced rather than assumed. Thirteen checks across two runs,
none failing.

## Compliance

- The body prescribes mechanism — the process count and the memory blob backend
  are implementation choices the story rules place in decisions, not user needs.
- The "so that" clause is circular and qualitative ("the deployment is genuinely
  unified") rather than a benefit a reader can settle by looking; the compliant
  text names what the operator gets, e.g. "As an operator running the all-in-one
  deployment, I want a deployment that needs no external storage service, so that
  I can run work end-to-end from a single container on one machine."
