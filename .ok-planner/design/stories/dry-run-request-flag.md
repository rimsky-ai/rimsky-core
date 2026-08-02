---
story: dry-run-request-flag
---

# Operator previews any write per-request

## Story

As an operator about to make a potentially destructive change, I can submit any write request with a per-request dry-run flag and get back a synthetic envelope showing what would have happened — same validation as a live write, no persistence — so that I preview the effect before committing.
