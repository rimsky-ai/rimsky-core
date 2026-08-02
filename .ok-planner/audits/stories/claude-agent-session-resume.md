---
audit: claude-agent-session-resume
artifact: story:claude-agent-session-resume
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:41Z
---

# CLI session continuity within one frame's RunScope, fresh in a sub-graph

Supported. An end-to-end scenario test drives a self-looping worker three times inside one frame and confirms all three dispatches share one `run_scope_id`, that each successive dispatch's fake CLI child reports being resumed with the prior dispatch's run id, and that its recall of an earlier turn's fact ("Alpha") survives across the loop — proving Success-leg continuity is real conversational carry-forward, not just an opaque token round-trip. The same test then drives a sub-graph invocation and confirms the child RunScope's single dispatch has a `run_scope_id` distinct from the parent's, arrives with no resumed-with id and no recalled fact, i.e. starts a fresh CLI conversation, matching the story's "fresh in a sub-graph" half exactly.
