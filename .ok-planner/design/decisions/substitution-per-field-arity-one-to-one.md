---
decision: substitution-per-field-arity-one-to-one
---

# A substitution directive names one source value

## Choice

Each substitution directive resolves against exactly one source value. The grammar admits no multi-source array form; several directives inside one field concatenate their values and never sum them. Many-to-many fan-in lives on the cascade surface, where a receiver subscribes to several senders (see `concept:attribute`, `concept:node-subscription`).

## Rationale

The two surfaces answer different questions. A subscription sums signals across upstreams and decides when a receiver runs. Substitution names what one field holds. Keeping fan-in on the subscription surface leaves one place to read a receiver's upstream set, so the declared edges describe the graph. A gathering directive would express fan-in a second time, and the two expressions could then disagree about one receiver. The arity asymmetry is the intent, not an omission.

## Alternatives

- Admit a multi-source array form so one field gathers values across upstreams — rejected: fan-in is then declared twice, on a surface that cannot also say when the receiver runs.
