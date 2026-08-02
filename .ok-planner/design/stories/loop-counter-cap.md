---
story: loop-counter-cap
---

# Bounded iteration via the loop-counter utility node

## Story

As a template author, I can use the bundled loop-counter utility node kind with a maximum-count input attribute, and observe it emit a `loop` tag on each dispatch while count is below max and a `done` tag when count reaches max, so I can express bounded iteration without authoring a custom executor.
