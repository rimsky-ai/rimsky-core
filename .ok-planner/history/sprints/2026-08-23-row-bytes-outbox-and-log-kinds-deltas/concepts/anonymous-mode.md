---
concept: anonymous-mode
---

# Anonymous mode

## What it is

Anonymous mode is a deployment state rimsky derives from its own data: the api-key ledger holds no active key. While the deployment stands in this state, rimsky admits a request that presents no credential as a synthetic administrator identity with unrestricted permission. Rimsky validates a request that does present a credential the same way in every state, so it rejects a malformed, unknown, expired, or revoked credential as unauthorized instead of promoting that client to administrator. The state ends the moment the deployment holds its first active key, and it returns when an operator revokes the last active key. Nothing configures the state: rimsky evaluates the predicate over the ledger on each credential-less request and substitutes the synthetic identity whenever it holds.

## Purpose

Anonymous mode solves the bootstrap problem. A fresh deployment holds no key, so it can authenticate nobody, and the surface that mints the first key would stand unreachable. Anonymous mode is the floor that lets an operator mint that first key through the same surface every later key comes from, instead of a separate database-only bootstrap path.

## Boundaries

Anonymous mode owns the predicate over the api-key ledger, the synthetic identity it substitutes, and the recurring operator-facing warning it raises while the deployment stands in this state. It owns no configuration setting: the state is computed, and no setting turns it on or off, so an operator leaves it by minting a key and returns to it by revoking every key. It does not own the api-key lifecycle itself, and it does not own how a dispatch reaches a particular daemon — an instance created while the deployment is anonymous carries the routing identity of the daemon it targets, and that identity belongs to `concept:host-daemon-proxy`. Recovery from the loss of every active key lies outside rimsky's request surfaces; it is an operator act on the ledger.

see also: `api-key`, `permission`, `host-daemon-proxy`, `rimsky-yml`
