---
audit: verifier-severity-partition
artifact: story:verifier-severity-partition
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Warning-severity findings tolerated, error-severity findings blocking

Supported. Three templates against a zero-config all-in-one deployment covered
both sides of the partition and the control between them: a failing
warning-severity check beside a passing error-severity check left the node
fresh while still counting the finding and naming it with its kind and
severity; the same check relabelled error, over the same rows, left the node
failed with the terminal error naming that check; and rows tripping both
checks blocked on the error-severity one, with the blocked terminal carrying
the warning-severity finding beside the blocking failure in one record. Since
the second leg changed nothing but the label, the severity label is what
decides.
