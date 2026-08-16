---
audit: no-resume-context
artifact: decision:no-resume-context
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:26:44Z
---

# The executor wire has no resume-context channel; park state rides scratch

Supported. The executor protocol retires all three of the fields the decision says are absent — the dispatch request's resume-context field, the park outcome's payload, and the park outcome's session token are each reserved by number and name, and no resume-context message exists. Park carries a scratch field and its attributes-delta field is likewise reserved, matching the claim that park performs no attribute writeback; the park terminal handler writes only scratch and the park state transition, never attributes. The supervisor parks the active run row in place and persists the scratch on it, the wake path resumes that same row rather than copying it, and the dispatch request built at acquisition carries the loaded scratch back to the executor. Unit tests cover the scratch write on terminal (inline, spilled, and empty cases) and the load back into an acquisition, and an executor-side conformance scenario exercises the park-and-resume scratch round trip against the reference stub.
