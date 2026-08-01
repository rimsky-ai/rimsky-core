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

Rimsky's services that hand out exclusive leases on data items used to be called "stores"; they are now "claim producers," and a project-wide sweep renamed every shipped, user-facing surface to match. The sweep didn't touch internal test machinery, where "store" survives: a test-fake helper package (`code:lib/foundation/locks/storetest`), test-fixture names (`ds-store`, `held-giveup-store`), a test-database name, container-internal mount paths. Nothing declares whether those leftovers are vocabulary the sweep missed or a boundary it drew on purpose — and re-verification confirms the corpus records neither: no decision documenting the rename sweep exists at all (`concept:claim-producer` lists `claim-store` only as a retired alias), so the premise the previous ruling leaned on — "state the boundary in the decision that records the sweep" — has no target file.

The complicating fact is that "store" also has a second, legitimate meaning in the tree: each producer's own internal storage layer is a live `package store` (`code:lib/services/claim_producers/filesystem/store/`), coexisting with claim-producer vocabulary by design. So the test names aren't unambiguously stale — some use the word in the storage-layer sense — and any boundary statement has to keep the two senses apart.

## Options

- **Author a small decision recording the sweep and its boundary** — shipped/user-facing surfaces carry claim-producer vocabulary; internal test scaffolding and the storage-layer packages are exempt. One new file; the question can't reopen.
- **Extend the sweep into the test machinery** — invasive across many test files, and no template author or operator ever observes the current names.
- **Leave undocumented** — free; the same audit finding refiles next cycle.

The ruling decides where the boundary sits and whether it gets written down.

## Ruling

> Recommended ruling (/verify-issues): author a small decision
> recording the store→claim-producer vocabulary sweep with its
> boundary at the shipped/user-facing surface — the test-fake
> package, fixture names, harness database names, and
> container-internal paths are exempt, as are the storage-layer
> `store` packages, whose different sense the decision states.
>
> Rationale: the churn of renaming internals buys nothing any user
> observes, and the tree already tolerates storage-layer "store"
> beside claim-producer vocabulary — but only a written boundary
> stops this finding from refiling forever, and no home for it
> exists today. The flip case: if the test fakes ever ship (e.g. as
> a public conformance kit), their names become user-facing and the
> exemption inverts.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
