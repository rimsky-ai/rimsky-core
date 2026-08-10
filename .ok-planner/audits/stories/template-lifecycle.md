---
audit: template-lifecycle
artifact: story:template-lifecycle
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:20:00Z
---

# Register, deploy, instantiate, retire and remove a template through the CLI

Supported. Against a zero-config all-in-one deployment, all 5 curation steps the
story names answered in one run: `template register` returned a template id and
the catalog listed it as registered; `template deploy` moved it to deployed and
`instance create` — refused before the deploy — then returned a live instance;
`template undeploy` moved it to undeployed, after which every further create was
refused; and `template rm` removed it from the catalog. Retirement and removal
are both guarded by what is still using the template: `undeploy` is refused while
an instance is live, `rm` is refused while the template is deployed, and `rm` is
refused again while a terminated instance's record still references the template,
succeeding once that record is deleted. That last refusal arrives as HTTP 500
carrying the persistence layer's raw foreign-key text rather than a conflict
naming the referencing records; the operation is correctly refused and the story
promises nothing about the diagnosis.
