---
audit: substitution-grammar-fallback-routing
artifact: decision:substitution-grammar-fallback-routing
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:38:09Z
---

# Every absent substitution source routes through one fallback / lenient path

Supported. The directive resolver parses the shared pipe-and-marker grammar once, resolves the body, and on a missing-source failure applies the author's declaration: a literal fallback yields the parsed literal, a lenient marker yields null, and neither re-raises the original error; any non-missing-source failure (a multi-pipe chain, a lenient-plus-fallback conflict) bypasses the path and hard-fails. The two absence causes the decision names converge before that point: the dispatch-time dependency builder walks the receiver's declared senders and simply omits any sender that is out of the run scope and any sender with no settled run in scope, so both arrive at the resolver as the same missing-source condition on the same directive, with no cause-specific branch anywhere in the resolver. Checked all six resolver source kinds — every one reports absence as the same missing-source error type. Unit tests cover the literal, null, number, bool, quoted-pipe, chain-rejection, and lenient-conflict cases, and end-to-end scenarios drive both a lenient recovery and a fired literal fallback through a real instance.
