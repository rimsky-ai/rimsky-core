---
issue: store-vocabulary-test-fake-boundary
kind: audit
category: inconsistent
artifacts:
  - decision:pre-v1-break-freely
status: verified
opened: 2026-07-25T03:18:31Z
---

# The "store" rename stopped at the test suite — deliberately, or unfinished?

Rimsky's services that hand out exclusive leases on data items used to be called "stores"; they're now "claim producers," and a recent project-wide sweep renamed every shipped, user-facing surface — binaries, entrypoints, example configs — to match. The sweep didn't touch internal test machinery, where "store" still appears: a test-fake helper package (`storetest`), test-fixture names (`ds-store`, `held-giveup-store`), a test-database name, and container-internal mount paths used only by tests. Nothing declares whether those leftovers are vocabulary the sweep should have caught or a boundary it drew on purpose.

The complicating fact is that "store" also has a second, legitimate meaning in the tree: each producer's own internal storage layer (filesystem, Postgres) is a package named `store` today, coexisting with the producer-level "claim-producer" name by design. So the test names aren't unambiguously stale — some of them may be using the word in the narrower storage-layer sense. The corpus is silent on where a rename sweep stops between user-facing and internal-only names; only the general pre-v1 permission to break things freely bears at all.

## Options

- **Extend the sweep** — declare the test helper, fixtures, and database/path names claim-producer vocabulary and rename them in a scoped follow-up; invasive across many test files, buys internal consistency.
- **Exempt them explicitly** — declare them internal-only naming (never seen by a template author or operator), consistent with the storage-layer packages that already keep "store," and record the boundary so the question doesn't reopen.

The ruling decides where the rename's boundary sits and gets it written down.

## Ruling

> Recommended ruling (/recommend-rulings): The sweep's boundary is the
> shipped/user-facing surface: storetest, its fixture names, harness
> DB names, and container-internal mount paths are exempt, stated
> explicitly in the decision that records the vocabulary sweep.
>
> Rationale: No template author or operator ever observes these names,
> so the churn buys nothing user-facing — and the tree already
> tolerates storage-layer 'store' (claim_producers/*/store) beside
> claim-producer vocabulary, so the exemption is consistent, not a new
> inconsistency.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
