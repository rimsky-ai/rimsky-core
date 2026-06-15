---
story: anonymous-mode-bootstrap
status: as-is
---

# Fresh deployment opens then locks down

## Role

As an operator bringing up a fresh rimsky deployment on a dev machine, I can use it without minting credentials first — anonymous mode is open, every action succeeds — and the moment I mint the first admin key, anonymous mode closes and subsequent unauthenticated requests are refused, so that I can experiment freely on first run and lock down the moment I'm ready.

## Capability

Anonymous-mode default on an empty keys table: open access; the first admin-key mint atomically closes anonymous mode.

## Business value

Operators experiment freely on first run without ceremony, then lock down deterministically the moment they mint the first key.

## Acceptance

Against a fresh deployment with no api-keys, requests through the control-api and CLI succeed without bearer tokens; the bootstrap operation mints the bootstrap admin key (plaintext returned exactly once) and from that point unauthenticated requests are refused; the status surface accurately reports the deployment's auth mode throughout.

## Falsifier

Anonymous mode stays open after a key is minted, OR the bootstrap operation succeeds on a deployment that already has keys, OR the status surface lies about which mode is active.

## Proof

Executable proof.
