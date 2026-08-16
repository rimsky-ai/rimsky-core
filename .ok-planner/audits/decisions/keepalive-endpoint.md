---
audit: keepalive-endpoint
artifact: decision:keepalive-endpoint
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T05:21:02Z
---

# A dedicated keepalive route whose stated single side effect is not the only one

Unsupported, because the handler performs two persisted mutations where the Choice promises one. Everything else holds: the supervisor registers a dedicated keepalive route separate from the attribute-writeback route, keyed by run identifier in the path; the handler never reads a request body; it answers with the same no-content status the attribute-writeback handler uses; and it authenticates with the dispatch's existing cancel token, comparing the bearer value in constant time against the same supervisor-and-run string the dispatch request stamps as its cancel token, which was checked against the construction site. But alongside bumping the dispatch's last-progress timestamp, the same transaction also renews the claim-handle expiry for every claim held by that run, pushing the lease out by the liveness interval. That is a second effect on a different table with real consequence — it is the difference between a held claim being reaped and surviving — and a reader working from this decision would not know a keepalive extends claim leases. It is also the one behaviour of this endpoint that nothing exercises: of the twelve keepalive tests, covering five authorisation rejections, three mutual-TLS cases, invalid and unknown run identifiers, the bump-failure path, success, and clock injection, none constructs a server with a claim-handle table, so the renewal branch short-circuits in every one of them.
