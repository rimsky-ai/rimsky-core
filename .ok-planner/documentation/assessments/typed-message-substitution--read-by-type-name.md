---
assessment: typed-message-substitution--read-by-type-name
subject: story:typed-message-substitution
way: read-by-type-name
release: d977250c
outcome: held
warrant: experiment:typed-message-substitution
---
# Reading a message body by the type's declared name, with no mixing between types

The audit woke one node that subscribes to two declared message types, once by each type in turn. In the frame the first type opened, the node resolved that type's field and fell back to its literal for the other; in the frame the second type opened, it resolved the other way round. A node that could react to several types therefore disambiguates by declared name and never mixed them. A directive reading a field the declared body schema does not carry is refused at registration, so the type name is a real contract rather than a label the author can misspell into a silent empty value.

## Unverified remainder

Two declared types on one node were exercised. The demonstration does not establish what a node resolves when two messages of different declared types open the same frame.
