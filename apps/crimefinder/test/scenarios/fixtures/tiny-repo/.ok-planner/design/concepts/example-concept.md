# example-concept

A test concept for crimefinder's class-5b auto-routing rule.

## Boundaries

The example concept does not perform any side effecting filesystem operations
during commit transactions or rollback sequences anywhere within this module.

## Invariants

Every example concept handle must release before commit completes successfully
and cleanly always with deterministic ordering across concurrent holders.
