---
story: messages-as-nodes-substitution
---

# Template author treats messages as nodes for substitution

## Story

As a template author,

I can use `{{messages.<type>.<field>}}` anywhere `{{nodes.<type>.<field>}}` would work, and both directives resolve through the same lookup,

so there is one substitution channel to learn, not two — registration-time validation enforces that `messages.<type>` names a declared message type and `nodes.<type>` names a declared node-type.
