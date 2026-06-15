---
story: terminate-after-run
status: as-is
---

# Operator opts a one-shot instance into self-termination

## Role

As an operator running a one-shot dev-loop invocation — typically
through the run-template CLI verb or an equivalent direct
instance-creation call — I can pass an opt-in terminate-after-run
flag so the instance self-terminates once its nodes settle, so that a
terminal-flag polling client (the shipped watch verb, a
CI gate, an MCP wait-for-terminal call) exits cleanly without me
having to issue an explicit termination.

## Capability

Per the durable-by-default instance lifecycle, an instance whose
terminate-after-run flag is unset stays alive across dispatches
and only terminates when the operator explicitly issues a
terminate. Setting the terminate-after-run flag at create time
opts into the opposite shape: the instance runs its graph exactly
once, and at the frame-end after that run the strict terminal
predicate fires and the instance's termination timestamp is
stamped. The flag is wired through both the control-API's
instance-creation surface and the run-template CLI verb's
terminate-after-run flag; the run-template verb's transient mode
implies it, so the dev-loop verb stays coherent end to end.

## Business value

The two operator stances — *one-shot dev loop* and *long-lived
durable instance* — share the same CLI verb and the same control
API. A new operator following the shipped first-steps walkthrough
drives the run-template verb and the watch verb, the watch loop
exits, the walkthrough ends cleanly. An operator building a
long-lived publisher-driven topology omits the flag, the instance
stays alive across fires, and the durable-by-default invariant on
`concept:instance` is preserved.

## Acceptance

A template with a single executor node, deployed and instantiated
with the terminate-after-run flag enabled on the create body. The
initial frame dispatches the node, the executor settles a real
terminal-success signal, and at the frame-end the instance's
termination timestamp is non-null. A subsequent message emit
against the now-terminated instance is rejected as a
terminal-state conflict. An equivalent run without the flag
leaves the instance's termination timestamp unset past node
settlement and accepts subsequent messages, preserving the
durable-by-default shape.

## Falsifier

The instance's termination timestamp is unset after the single run's
node settles, OR the instance accepts a follow-up message after
settlement, OR the flag is silently ignored on the create path,
OR the run-template verb's terminate-after-run flag plumbs through
but the control-API create body doesn't carry the field, OR the
durable-by-default shape regresses for an instance created without
the flag.

## Proof

Executable proof — deploy a single executor template against the
real assembled product, create an instance with the
terminate-after-run flag enabled, drive the executor to a real
terminal-success through the bundled stub, then read the instance
back and assert the instance's termination timestamp is set and a
follow-up message emit is rejected as a terminal-state conflict.
The durable-by-default counter-shape is pinned by the sibling
proof on `story:sensor-http`, which exercises the no-flag path and
asserts the instance stays alive across N real fires. Together
the two proofs pin both lifecycles end to end against the real
assembled product.
