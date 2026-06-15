---
story: claude-agent-session-resume
status: as-is
aliases: []
---

# CLI session continues within a RunScope, fresh in a new RunScope

## Role and capability

As a template author, I can wire claude-agent so its agent CLI conversation continues across multiple dispatches of the agent node within a RunScope and starts fresh in a new RunScope, so build/validate orchestrators preserve agent reasoning continuity within a pass and reset across passes.

## Acceptance

I declare a claude-agent node in a graph; cascade re-fires the node multiple times within one RunScope; each post-first dispatch continues the same CLI conversation from the prior turn (agent has the prior turn's context). A sub-graph invocation of the same template starts the agent with a fresh CLI conversation, no carried session from the parent scope.

## Falsifier

A re-dispatch within the same RunScope starts a brand-new CLI conversation (agent has no recollection of the prior turn). OR: a sub-graph invocation inherits the parent scope's CLI session.

## Proof

Demo — scenario test using the bundled-services integration harness that runs claude-agent through three dispatches in one RunScope (agent's responses must reference content from prior turns), then invokes a sub-graph and observes the agent starts fresh.
