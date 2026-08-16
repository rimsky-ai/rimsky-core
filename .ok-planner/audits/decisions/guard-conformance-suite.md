---
audit: guard-conformance-suite
artifact: decision:guard-conformance-suite
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:52:00Z
---

# A shared wrong-claimant suite asserted identically against both drivers

Supported, and the "every" was checked by enumerating the operations rather than the tests. The driver-parity library exposes one suite function invoked from a single file by two entry points — one opening a live client-server database, one opening a fresh embedded file — so every assertion is the same source run twice, which is the point of the choice. Its guard section registers seventeen cases, and enumerating the guarded mutators from the two persistence interfaces gives thirteen claim-handle operations and seven node-run ownership operations: all twenty have a wrong-supervisor case in the shared library asserting the operation changes nothing, with the disposition-carrying release covered in the recovery-dispatch section of the same suite rather than in the guard section itself. The suite also pins the negative side deliberately — one case exercises the force-override variants that carry no guard by design, and one enumerates the holder-retirement operations that are carve-outs, so a reader can tell an intentional gap from an omission. Both rejected alternatives are absent: the wrong-claimant assertions exist nowhere outside the shared library, so there are no per-driver copies to drift, and the guard is proven behaviourally rather than by trusting the helper alone.
