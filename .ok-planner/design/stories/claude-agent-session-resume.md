---
story: claude-agent-session-resume
status: as-is
aliases: []
---

# CLI session continues within one frame's RunScope, fresh in a sub-graph

## Role and capability

As a template author, I can wire claude-agent so its agent CLI conversation continues across multiple dispatches of the agent node that share one frame's RunScope (typically cascade self-edges within a single frame) and starts fresh in a child RunScope (sub-graph invocation), so cascade-driven agent loops preserve reasoning continuity across re-fires within a frame and reset across sub-graph boundaries. Cross-frame session continuity is out of this story's scope: a template that wants an agent's CLI session to span frames must ferry the session token through a message body via the standard message-borne cross-frame coupling path (see `concept:message`).

## Acceptance

I declare a claude-agent node in a graph; cascade re-fires the node multiple times within one frame's RunScope (e.g. via a self-edge subscription); each post-first dispatch continues the same CLI conversation from the prior turn (agent has the prior turn's context). A sub-graph invocation of the same template starts the agent with a fresh CLI conversation, no carried session from the parent scope.

## Falsifier

A re-dispatch within the same frame's RunScope starts a brand-new CLI conversation (agent has no recollection of the prior turn). OR: a sub-graph invocation inherits the parent scope's CLI session. OR: the session-token attribute carries across frame boundaries (it must not — cross-frame state moves only via messages).

## Proof

Demo — scenario test using the bundled-services integration harness that runs claude-agent through three dispatches in one frame's RunScope (cascade self-edge fires the node three times within the same frame; agent's responses must reference content from prior turns), then invokes a sub-graph and observes the agent starts fresh.
