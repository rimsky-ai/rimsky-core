---
issue: service-address-book-reload-absent
kind: audit
category: conflicting
artifacts:
  - concept:service-address-book
status: verified
opened: 2026-08-16T09:05:04Z
---

# The service address book promises republication on configuration reload; no reload exists

The service address book (the catalog of declared executor and store endpoints every supervisor resolves against) is published by the control plane; the concept says at startup and on configuration reload. There is one publish call, at control-API start; no reload verb, signal handler or watcher exists anywhere. Until the control plane restarts, supervisors resolve names against a catalog that no longer matches a changed configuration file. The ruling decides whether reload is built or the promise withdrawn.

## Options

- Narrow the concept to startup-only and say a declaration change takes effect on the next control-plane start; cost: a promise operators may read as "hot-swap is safe" is withdrawn.
- Build a reload trigger (a signal or admin route that republishes in one transaction); cost: a feature with its own design questions — what triggers it, and what a failed republish leaves behind.

The ruling decides whether configuration changes need a restart.

## Ruling

> Recommended ruling (/verify-issues): Narrow the concept to startup publication and say plainly that a changed declaration takes effect on the next control-plane start; file reload as future work only if an operator story asks for it.
>
> Rationale: nothing else in the deployment reloads configuration live (the strict loader runs at start), so a reload promise on one catalog is an outlier the corpus made by accident; the honest text costs nothing and the operator behaviour is already restart-shaped. Flip case: a story about rotating an executor endpoint without downtime would make the second option the right one.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
