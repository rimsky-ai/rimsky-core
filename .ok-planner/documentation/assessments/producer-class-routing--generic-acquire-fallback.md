---
assessment: producer-class-routing--generic-acquire-fallback
subject: story:producer-class-routing
way: generic-acquire-fallback
release: d977250c
outcome: held
warrant: experiment:producer-class-routing
---
# The generic acquire-family key as the fallback, with the specific key winning

With no producer-class entry in the template's error-class map, the generic acquire-family key routed the same producer failure — so an author who has not enumerated a producer's classes still gets a route. With both declared, the producer-class entry decided rather than the generic key beside it: specific above, fallback below. That precedence is what makes the fallback safe to leave in place while adding specific routes over time.

## Unverified remainder

That the generic acquire-family keys behave as a fallback is settled by this run; whether they are written down as one anywhere in the product's own material is a question for its documentation discipline and is not settled here.
