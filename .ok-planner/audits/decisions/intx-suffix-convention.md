---
audit: intx-suffix-convention
artifact: decision:intx-suffix-convention
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T04:47:25Z
checked: 7
unaccounted: 2
---

# Whether the suffix means "requires an open transaction" and no forbidden pairs survive

Unsupported by two of the seven. The no-pairs half holds outright: walking the persistence layer's declarations found no public method coexisting with a private same-base sibling, the blob backend is the capability split the choice describes — a transactional interface embedding the plain one and adding two suffixed methods, with the Postgres large-object backend implementing both — and a fitness test parses the whole layer and fails on any receiver carrying both variants outside that split. The suffix-meaning half does not hold. Of the 7 suffixed declarations in the layer, 5 genuinely require the transaction — the two interface methods, their two Postgres implementations, which fail when the handle does not unwrap, and the conformance park-resume helper, which passes it straight to the store — but two exported helpers branch on whether the transaction is nil and fall back to the non-transactional path, which is the optional-parameter job the choice says gets the other spelling. They also sit outside the fitness test's reach, since it inspects only methods with receivers.

## Unaccounted

- The exported blob write helper takes the transaction as optional, using it when present and falling back to the plain backend write when nil, while wearing the requires-a-transaction suffix.
- The exported blob read helper does the same on the read path.
