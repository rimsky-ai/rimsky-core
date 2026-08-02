---
audit: portable-template-across-modes
artifact: story:portable-template-across-modes
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:05Z
---

# The same template file reaches an equivalent terminal shape in all-in-one and multi-container modes

Supported. `TestPortableTemplateAcrossModes` (`lib/services/test/scenarios/portable_template_across_modes_e2e_test.go`) builds one template JSON body and dispatches the identical byte-for-byte bytes twice: once through an in-process ("all-in-one" style) rimsky brought up with `harness.BringUpRimskyHandle` plus a bundled in-process filesystem claim-producer and stub-mode http-node, and once through a fully containerized deployment on a shared Docker network with the http-node and filesystem-claim-producer services running as separate containers. Both runs assert the same terminal node type, the same terminal tag class, and that the JSON-Schema `default` for `stub_probe` resolved identically in `latest_attributes` — i.e. no dev-vs-prod dialect and no divergent schema-default handling between the two deployment topologies for one unedited template file.
