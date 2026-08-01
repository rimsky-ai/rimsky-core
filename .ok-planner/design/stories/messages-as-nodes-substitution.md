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

