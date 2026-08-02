---
story: resume-preserves-snapshot
---

# Parked node-run resumes against its dispatch-time inputs

## Story

As a template author relying on a parked node continuing where it left off, I can rely on the resumed executor seeing the same substituted upstream values it saw when it parked — even if upstream nodes re-ran during the park — so that parking works as a continuation of one unit of work rather than a re-evaluation with silently rewritten inputs.
