---
story: anonymous-mode-bootstrap
status: as-is
---

# Fresh deployment opens then locks down

## Story

As an operator bringing up a fresh rimsky deployment on a dev machine, I can use it without minting credentials first — anonymous mode is open, every action succeeds — and the moment I mint the first admin key, anonymous mode closes and subsequent unauthenticated requests are refused, so that I can experiment freely on first run and lock down the moment I'm ready.

Anonymous-mode default on an empty keys table: open access; the first admin-key mint atomically closes anonymous mode.

Operators experiment freely on first run without ceremony, then lock down deterministically the moment they mint the first key.
