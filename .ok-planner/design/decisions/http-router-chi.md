---
decision: http-router-chi
status: as-is
---

# HTTP routing

## Choice

The go-chi/chi router, pinned to a stable major line.

## Rationale

Lightweight and net/http-native: chi composes with standard `http.Handler` middleware and adds no framework abstractions.

## Alternatives

- Heavier opinionated web frameworks — rejected: pull in middleware stacks and abstractions the project does not need.
- The stdlib mux alone — rejected: lacks the route grouping and middleware composition the control surface needs.
