---
story: template-error-policy
---

# Template author routes error classes

## Story

As a template author writing fault-tolerant workflows, I can declare per-error-class routing actions (pass, give-up, retry, release-and-requeue) and have the runtime honor each action at the appropriate error site, so that I express graceful failure handling without writing handlers.
