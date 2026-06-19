---
story: sub-claim-payload-substitution
status: as-is
---

# Template author reads per-sub-claim payload through the standard claim directive

## Role

As a template author,

## Capability

I can read producer-supplied per-sub-claim data via `{{claim.<alias>.payload[.<field>]}}` in a fan-out child's substitution context, and the path resolves identically to how it resolves on a regular Open'd claim,

## Business value

so there is no second mechanism to learn for sub-claims.

## Acceptance

I author a fan-out template; the producer returns per-sub-claim `payload` bytes; the child's attribute substitution reads the per-sub-claim payload via the standard `{{claim.<alias>.payload.<field>}}` directive.

## Falsifier

`{{claim.<alias>.payload}}` returns "empty" or "not found" for sub-claims; OR resolves to the parent's payload rather than the sub-claim's.

## Proof

Executable proof. Per-sub-claim payload visible in each child's executor run.
