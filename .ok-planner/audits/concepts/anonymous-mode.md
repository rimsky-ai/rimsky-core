---
audit: anonymous-mode
artifact: concept:anonymous-mode
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:06:31Z
---

# Anonymous mode as a data-derived open-on-first-run state, and the seven invariants guarding its edges

Supported. All seven invariants hold. The mode is computed on every credential-less request from a count of active api-key rows and nothing else — there is no configuration key anywhere that turns it on or off, and a presented credential is validated normally in either mode (malformed, unknown, expired, revoked all deny). A recurring warning fires from a background watcher started with the control plane and stops as soon as any active key exists. The per-replica predicate cache carries a one-second lifetime plus a generation counter bumped by every key mutation; all three mutating auth handlers bump it, and the rotation-grace sweep bumps it too through a registered hook — harmless, since the active-key count already excludes rows past their scheduled revoke time, so the sweep cannot change the predicate. Revoking the last active key is refused in the store layer unless an explicit intent flag rides the request; minting a deployment's first key with an expiry, and rotating its sole active key, are each refused the same way, while permanent keys and mints alongside other active keys pass untouched. Instances created without an owning key are stamped with the target agent's generated name at creation, persisted in a dedicated column across both storage backends, and dispatches resolve through the same routing field used for key-owned instances; the proxy admits several anonymous agents at once, rejecting a name collision rather than displacing the incumbent and retrying generated names, so concurrent agents do not interfere. No verb or endpoint restores access after total key loss: the only mint path itself requires a permission, which requires a key. Scenario suites cover the bootstrap walk end to end, the banner starting and stopping, cache invalidation on mint, on revoke and on sweep, the last-key guard including its concurrent race, and the anonymous agent registration paths.
