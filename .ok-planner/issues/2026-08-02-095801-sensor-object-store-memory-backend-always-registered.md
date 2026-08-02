---
issue: sensor-object-store-memory-backend-always-registered
kind: audit
category: decision-drift
artifacts:
  - decision:object-store-watching-model
status: verified
opened: 2026-08-02T09:58:01Z
---

# The in-memory object-store backend ships in every production sensor image

The bundled object-store sensor (a poller that watches a bucket-like store for new deposits) ships with a backend the design says must not ship. Its process entry point registers the in-memory backend unconditionally, right before the filesystem backend's env-gated registration, so every deployed binary and image advertises `memory` as a selectable backend in its capabilities schema (`code:lib/services/sensors/sensor-object-store/main.go`). The governing decision commits to the opposite: the sensor "ships only the filesystem backend," with the in-memory one "a test fixture, not a shipped store" (`decision:object-store-watching-model`).

The asymmetry comes from how the two registrations are gated. The filesystem backend needs a root path, so its registration naturally hangs off an environment variable (`env:RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT`); the memory backend needs no configuration at all, so nothing ever forced a gate around it. An operator who selects `memory` in production gets an unbounded, non-persistent store that silently loses its watch state on every restart — nothing in the sensor warns them off, and the capabilities enum actively invites the choice.

The watching mechanics themselves are sound and audited supported; the only gap is that the fixture is reachable in production. The ruling decides whether the fixture gets fenced off or the decision blesses shipping it.

## Options

- Gate the memory backend's registration behind an explicit enable (an env var the tests and local dev set). Cost: the flag is artificial — unlike the filesystem gate, nothing about the backend needs it, so it exists purely to hide a fixture.
- Amend the decision to acknowledge the memory backend ships. Cost: legitimizes an unbounded, restart-amnesiac backend as production-selectable with no named use case.

## Ruling

> Recommended ruling (/verify-issues): gate the memory backend behind an explicit env enable, set by the tests and anyone doing throwaway local dev, so the shipped default capabilities enum lists only `filesystem`. The decision stands as written.
>
> Rationale: the decision's fixture-not-shipped line is the project's actual intent — nobody has named a production use for a store that forgets everything on restart, and the amend option only exists because editing prose is cheaper than adding a flag. The artificial-flag cost is real but tiny and self-documenting. Flip case: if a zero-config demo or quickstart flow turns out to depend on selecting `memory` from the shipped image, bless it in the decision instead and name that use.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
