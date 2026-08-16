---
assessment: messages-as-nodes-substitution--one-lookup
subject: story:messages-as-nodes-substitution
way: one-lookup
release: d977250c
outcome: held
warrant: experiment:messages-as-nodes-substitution
---
# Reading a message body wherever a node's attribute can be read, through one channel

The audit had a single node source one attribute through the message form and another through the node form in the same template, and the node settled with both values resolved in the same dispatch. The value read through the message form appears as an ordinary attribute write on the message type's own node, which is what makes "the same lookup" a demonstrated fact rather than a claim: the declared message type is materialised as an ordinary node of the instance, so both forms resolve against the same thing. A template author therefore has one substitution channel to learn rather than two. Eight checks across this way and its sibling, none failing.

## Unverified remainder

Interchangeability was taken in the attribute-source context. The run does not enumerate every context in which a node reference is legal, so the two forms are demonstrated equivalent where an attribute is sourced rather than everywhere.
