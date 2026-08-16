---
assessment: fanout-any-substitution-source--host-environment
subject: story:fanout-any-substitution-source
way: host-environment
release: d977250c
outcome: held
warrant: experiment:fanout-any-substitution-source
---
# Writing a fan-out partition request that reads from a host-environment variable

With `catalog:template-keys/nodes[].fan_out.partition_request` reading from a host-environment variable of the running deployment, the fan-out dispatched exactly the two partitions that variable named, both work units resolved their own keys, and no resolution error was recorded. This is a fifth source beyond the four the promise enumerates, and it resolved on the same terms as the rest, which is what "the source is my choice and not the architecture's" asks for.

## Unverified remainder

The substitution grammar carries six source kinds. Five of them were driven here; the sixth, the per-child partition identifier, is the fan-out's own output and so cannot be an input to the request that creates the partitions. Nothing about that sixth kind is established as a partition-request source by this run.
