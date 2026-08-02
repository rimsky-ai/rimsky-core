---
decision: child-execution-naming
---

# The child-execution primitives carry plain names

## Choice

The child-execution primitives carry plain, descriptive names: a single shared dispatch-children primitive, and two named settle primitives — carry (sub-graph delegation's settle) and aggregate (fan-out's settle) — rather than one settle-children primitive covering both.

## Rationale

Descriptive naming avoids overloading "delegation." Settlement is named as two primitives, not one, because fan-out and sub-graph delegation are distinct mechanisms with different fan-in shapes (see `concept:child-execution`): a single settle-children name would imply one shape where there are two.

## Alternatives

- One settle-children primitive covering both fan-in shapes — rejected: implies a single settlement shape where fan-out and sub-graph delegation genuinely differ.
- Naming the primitives after "delegation" — rejected: overloads a term that names only one of the two mechanisms.
