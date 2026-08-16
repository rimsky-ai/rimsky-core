---
assessment: loop-counter-cap--terminal-round-marked
subject: story:loop-counter-cap
way: terminal-round-marked
release: d977250c
outcome: held
warrant: experiment:loop-counter-cap
---
# Telling which round of the iteration is the last one

Each dispatch of the bundled counter node carries a tag saying whether more rounds are coming or this is the final one, and the audit read those tags back through the deployment's node and run reads. At a maximum of four, the first three dispatches carried the iterating tag and the fourth carried the terminal one, so exactly one round in the sequence is marked as last. At a maximum of one the single dispatch carried the terminal tag directly, so the degenerate case is marked too rather than left ambiguous. A downstream node can therefore subscribe on the difference and act only on the final round.

## Unverified remainder

The marking was read at two bounds on a self-subscribed single-node template. The way does not establish that a downstream consumer filtering on the terminal tag runs exactly once in more elaborate graphs.
