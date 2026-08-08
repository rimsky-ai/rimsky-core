---
issue: whoami-mounted-but-absent-from-action-registry
kind: human
category: enforcement-gap
artifacts:
  - concept:control-api
  - concept:permission
  - concept:api-key
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:53:20Z
github: https://github.com/rimsky-ai/rimsky-core/issues/97
---

# A live, load-bearing route is missing from the registry that documents every route

The control API keeps an action registry: a table naming every route, its action,
and its permission. It is the canonical route table — it generates the tool
catalog agents read, it backs grant validation, and it is the source the
published REST reference is generated from. Sixty-five routes match it one for
one.

The identity-echo route does not appear in it. That route answers "who is this
token?" — it resolves the caller's identity and refuses an invalid token, but it
runs no per-action permission check, making it the only route besides the health
probe registered without a gate. And it is not decorative: the host-agent proxy
calls it on **every** agent registration, verifying the presented key and routing
by the key id the control API reports back. A production dependency, invisible to
all three registry consumers.

## Why it has no permission, and why nobody can find that out

There are three tiers of route, not two. The health probe needs no token at all.
The identity-echo route needs a valid token but no permission. Everything else
needs both.

The middle tier exists because no permission on this route would mean anything.
Every other route does something a caller might or might not be allowed to do;
this one hands back the identity of the credential just presented — name, kind,
key id, nothing else. Gating it would be asking whether a key may learn its own
name, and a denial would be unlearnable, since the only subject of the question
is the asker. So there is nothing to check.

That reasoning is not written anywhere. It is not in the route registration, the
handler, the control-API concept, or any decision. The one trace of intent is the
name of a test helper — the registry-coverage test carves out this route under a
name meaning "identity echo" — and a name asserts a category without justifying
it. So the exemption is deliberate in the thin sense that someone made a
carve-out on purpose, and undocumented in every sense that matters to a reader
deciding whether it is safe.

The corpus, meanwhile, says something that is currently false. The control-API
concept states that every operation is auth-gated except the health probe, and
that the registry is the canonical mapping with an unmapped operation being a
wiring bug. The doc knows only two tiers where there are three, so it has no
name for the middle one and no place to record why it exists.

Two smaller fidelity gaps sit in the same table. Two routes are mounted only when
their dependency is configured — one when a certificate authority is present, one
when observability is wired — but both are listed unconditionally. In a
deployment without them, a documented route 404s.

## Options

- **Make the registry complete over what's mounted**, carrying gating and
  conditional mounting as properties of an entry rather than as reasons to omit
  one. Fixes the observed harm — the route becomes visible to the docs, the tool
  catalog, and grant validation — and gives the two conditional routes an honest
  home. Costs a change to the entry shape, and creates a new kind of entry that
  no consumer currently expects.
- **Leave the code alone and amend the concept doc** to name the identity-echo
  route as a second explicit exception alongside the health probe, mirroring what
  the test already encodes. Cheap and accurate. Leaves the route absent from
  every generated surface, which is the harm that was actually reported.
- **Gate the route like any other action.** Uniform, and no new concepts. Changes
  behavior on a route whose entire purpose is answering an unauthenticated-ish
  "what am I?" question, and the host-agent proxy depends on it working before an
  agent has any granted permission at all.

The ruling decides whether the registry lists every mounted route, or documents
its exceptions elsewhere.

## Ruling

> Make the registry complete over the routes
> actually mounted, and make gating a property of an entry rather than a reason to
> leave one out. The identity-echo route gets an entry marked as
> identity-resolved-but-ungated, carrying the reason — a permission on it could
> only ask whether a key may learn its own name, and a denial would be
> unlearnable — because that reasoning exists nowhere in the tree today and a
> future reader will otherwise re-derive it or mistake the exemption for an
> oversight. The two conditionally-mounted routes get their mounting condition
> recorded the same way. Then correct the control-API concept to describe three
> tiers rather than two — no token, token without permission, token with
> permission — and to say what the registry now guarantees: it lists every mounted
> route, and each entry says how it is gated and whether it is always present.
>
> The second half is not optional: the grant-acceptance path must reject a
> permission naming an ungated action. Grant validation reads the whole registry
> today — key creation checks each requested action against the same flat entry
> map and 400s on anything unknown — so adding an ungated entry without that
> check would make the identity-echo action grantable, and an operator could
> write a permission for it that silently means nothing. Shipping the first half
> alone manufactures a worse defect than the one being fixed.
>
> Rationale: the reported harm is that a live production dependency is invisible
> to the docs, the agent tool catalog, and grant validation. Only this option
> removes it; the doc-amendment option records the exception accurately and leaves
> the route just as invisible, and gating the route touches behavior the host-agent
> proxy relies on before an agent holds any grant. It also generalizes: the
> conditional-mount gaps are the same defect — a table that omits or flattens
> whatever doesn't fit its shape — and one change answers both.
>
> One thing to check while drafting: whether keys can be edited after creation,
> or permissions written in other shapes, along paths that validate separately
> from the key-creation check. If they all share that one check, refusing ungated
> actions is a small change. If each validates on its own, this touches permission
> handling in several places for the sake of documenting one route, and correcting
> the concept doc alone becomes the better trade.
