---
story: message-schema
status: as-is
---

# Template author declares accepted message types

## Role

As a template author,

## Capability

I can declare which message types instances of this template accept,

## Business value

so that messages have a typed contract and unknown ones fail loud instead of silently dead-lettering.

## Acceptance

I write a template-level `messages:` block enumerating accepted types, each with a body shape. When a sender posts a message of a declared type, the instance opens a frame and the receivers I have declared via subscriptions stale-mark, substituting the body into their attribute schemas. When a sender posts a message of an undeclared type, the request is refused with an error naming the unknown type and listing the declared set.

## Falsifier

A message of an undeclared type lands in the ledger and is silently dropped; OR a declared message arrives and no subscribed node is marked stale.

## Proof

Executable proof. Declared type opens a frame and stale-marks subscribed receivers; undeclared type refuses with the expected error.
