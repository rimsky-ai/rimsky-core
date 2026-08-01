---
story: one-message-per-frame
status: as-is
---

# Template author relies on one message per frame for well-defined substitution

## Story

As a template author,

I can rely on substitution from the message body always being well-defined in a node that's reacting to a message,

so that no template ever has to refuse a multi-message coalesced frame at runtime.
