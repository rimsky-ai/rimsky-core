---
issue: standalone-validator-roles-not-from-capabilities
kind: audit
category: conflicting
artifacts:
  - concept:validation
status: verified
opened: 2026-08-16T08:59:37Z
---

# A standalone validator's roles come from its config declaration, not from its capabilities — and it has no capabilities to ask

Validation services check templates at registration for the roles they advertise; the validation concept says a validation-supporting service's capabilities advertise its role discriminators. For the three peer kinds (producer, executor, publisher) that holds — their own capabilities handshake carries the roles. For a service configured under the standalone validators block, roles are derived from the entry's declared protocols minus validation itself, and no handshake is fetched; an entry declaring only validation registers with an empty role set and is silently never consulted. There is a deeper fact: the validation protocol has exactly one RPC, so a validation-only service has no capabilities surface to query — the concept's rule cannot hold for it without a protocol change. The ruling decides whether the protocol grows a handshake or the concept and the loader change.

## Options

- Add a capabilities RPC to the validation protocol and dial it for standalone entries; cost: a wire-contract change every validator implementer must add.
- Amend the invariant so a standalone validator's roles come from its declared protocols, and make an empty resulting role set a load-time configuration error; cost: narrows the universal, but closes the silent failure with no protocol change.

The ruling decides where a standalone validator's roles are declared.

## Ruling

> Recommended ruling (/verify-issues): Take the second option — declared protocols are the roles for a standalone validator, and an entry whose derived role set is empty fails the deployment load with an error naming the entry; amend the concept to say the capabilities rule applies to peers that have a handshake.
>
> Rationale: a protocol change for a configuration-time fact is disproportionate, and the failure the run found is the silence, not the source of the roles. Flip case: if validation-only services are expected to advertise roles dynamically (a validator that learns new roles without a config edit), the handshake is the right home and the first option wins.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
