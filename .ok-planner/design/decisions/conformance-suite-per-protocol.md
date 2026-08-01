---
decision: conformance-suite-per-protocol
status: as-is
---

# Conformance discipline

## Choice

One conformance suite per protocol under the protocols module's conformance package, exposed as a per-protocol conformance subcommand on the CLI.

## Rationale

External implementations prove themselves against the platform's own suite rather than ad-hoc integration testing. Per-protocol packaging lets an implementer of one protocol certify without carrying the others, and the CLI subcommand makes the suite runnable against any endpoint.

## Alternatives

- One monolithic suite spanning all protocols — rejected: an implementer of a single protocol would carry the other protocols' requirements and fixtures.
- No packaged conformance kit, relying on in-tree scenario tests — rejected: external implementations cannot be validated by tests that only drive the bundled stack.
