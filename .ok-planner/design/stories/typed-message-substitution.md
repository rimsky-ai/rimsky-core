---
story: typed-message-substitution
status: as-is
---

# Template author reads message bodies through the typed-message substitution grammar

## Role

As a template author,

## Capability

I read from and compose message bodies using the same substitution grammar that handles node attributes, with each message type addressable by its declared name,

## Business value

so that I can disambiguate when a node could react to several types, and message bodies are first-class typed attribute blocks that flow across frames.

## Acceptance

A receiver node's attribute schema substitutes from a specific message type by naming the type in the substitution grammar, parallel to substituting from a specific node's attribute by naming the node-type. A message-sender node's attributes compose the destination message body by the same substitution grammar (sources can be other nodes' attributes, instance params, the triggering signal's payload). The substitution engine validates references at template registration in both directions: a receiver reading a field the declared body schema doesn't have rejects; an sender declaring an attribute field the destination type's body schema doesn't have rejects. The runtime resolves message-body reads through the same code path that resolves attribute reads.

## Falsifier

The grammar for substituting from messages differs in shape from the grammar for substituting from node attributes; OR the engine has a separate code path for message-body reads vs attribute reads; OR a typo in a message body field on either side registers without error; OR a receiver attribute schema can read from a message type without naming that type.

## Proof

Executable proof. Typo'd field names reject at registration in both directions; a running back-edge cycle's receiver reads through the typed-message grammar and resolves correctly; an assertion confirms a single substitution-resolution function services both surfaces.
