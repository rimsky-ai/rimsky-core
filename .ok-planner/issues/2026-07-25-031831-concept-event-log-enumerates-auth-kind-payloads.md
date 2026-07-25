---
issue: concept-event-log-enumerates-auth-kind-payloads
kind: audit
category: other
artifacts:
  - concept:event-log
status: verified
opened: 2026-07-25T03:18:31Z
---

# The audit-log concept spells out every field of every auth event — useful detail, wrong altitude

Rimsky's event log is its durable, append-only record of everything that happens — typed events, each with a kind (its category) and a payload (its data). The concept document defining it carries an "Auth event kinds" section that names all five authentication-related kinds by exact wire string (`auth.access_attempted`, `auth.key_revoked`, …) and lists every payload field verbatim — key ID, identity type, client IP, user agent, and so on. The house rule for concept documents forbids exactly this: wire-format enumeration belongs in code or in a decision document, because a concept that inventories fields must chase every field change forever.

What keeps this from being a simple deletion: the detail has real value. Auth events are a compliance-sensitive, deliberately closed vocabulary, and today this section is the one place an auditor can see at a glance what an access-denial record contains. Two sibling concepts recently established the deferral precedent ("field membership is owned by the emission code") — and, separately, the decisions catalog already contains a document about event-log payload shapes, which is the corpus's licensed home for naming exact wire detail.

## Options

- **Trim to a generic actor/action/target/result description**, deferring kinds and fields to code — matches the sibling precedent; the at-a-glance reference disappears.
- **Keep the enumeration as a carved-out exception** for the auth vocabulary specifically — preserves the reference, permanently exempts one section from the rule.
- **Relocate the enumeration into the existing payload-shapes decision** — auditors keep their reference at the altitude licensed to hold it; the concept returns to definition.

The ruling decides: defer, exempt, or relocate.

## Ruling

> Recommended ruling (/recommend-rulings): Relocate the auth.* kind
> and payload enumeration into the existing decision:event-log-
> payload-shapes (extending it), and trim the concept section to the
> generic actor/action/target/result description.
>
> Rationale: Decisions are licensed to name wire shapes — the artifact
> identity carries the compliance value here — so relocation preserves
> what auditors use while restoring concept altitude, per the
> signal/transition-reason precedent.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
