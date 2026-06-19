---
story: messages-as-nodes-substitution
status: as-is
---

# Template author treats messages as nodes for substitution

## Role

As a template author,

## Capability

I can use `{{messages.<type>.<field>}}` anywhere `{{nodes.<type>.<field>}}` would work, and both directives resolve through the same lookup,

## Business value

so there is one substitution channel to learn, not two — registration-time validation enforces that `messages.<type>` names a declared message type and `nodes.<type>` names a declared node-type.

## Acceptance

I author a template using `{{messages.<T>.body}}` where `<T>` is declared in the template's `messages:` registry; the semantics are identical to `{{nodes.<T>.body}}` for a node named `<T>`. A template declaring `{{messages.<U>.x}}` for a `<U>` not declared in `messages:` rejects at registration with a clear error naming `<U>` and the missing declaration.

## Falsifier

The two directives produce different errors or different values for the same underlying data; OR registration accepts undeclared message types.

## Proof

Executable proof. A runnable template using both forms; plus a registration-failure proof for the undeclared case.
