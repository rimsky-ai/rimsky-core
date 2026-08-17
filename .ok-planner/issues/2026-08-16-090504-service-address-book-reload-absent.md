---
issue: service-address-book-reload-absent
kind: audit
category: conflicting
artifacts:
  - concept:service-address-book
status: verified
opened: 2026-08-16T09:05:04Z
---

# The concept says the control plane republishes the service address book on configuration reload; no reload exists

The control plane publishes the service address book, the catalog of declared executor and store endpoints every supervisor resolves against. The concept says it publishes at startup and on configuration reload. The tree holds one publish call, at control-API start. No reload verb, signal handler or watcher exists anywhere. Until the control plane restarts, supervisors resolve names against a catalog that no longer matches a changed configuration file. The ruling decides whether the project builds reload or withdraws the promise.

## Options

- Narrow the concept to startup-only and say a declaration change takes effect on the next control-plane start; cost: withdraws a promise operators may read as "hot-swap is safe".
- Build a reload trigger, a signal or admin route that republishes in one transaction; cost: a feature with its own design questions, namely what triggers it and what a failed republish leaves behind.

The ruling decides whether configuration changes need a restart.

## Ruling

> Recommended ruling (/verify-issues): Narrow the concept to startup publication and say plainly that a changed declaration takes effect on the next control-plane start. File reload as future work when an operator story asks for it.
>
> Rationale: nothing else in the deployment reloads configuration live, because the strict loader runs at start, so a reload promise on one catalog is an outlier the corpus made by accident. The honest text costs nothing, and operators already restart to apply a configuration change. Flip case: a story about rotating an executor endpoint without downtime would make the second option the right one.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
