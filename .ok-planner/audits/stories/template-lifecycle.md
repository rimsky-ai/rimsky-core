---
audit: template-lifecycle
artifact: story:template-lifecycle
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:05Z
---

# Operator can register, deploy, instantiate, undeploy, and delete a template

Supported. The control API's `/v1/templates` route family (`lib/control/controlapi/templates.go`) implements the full lifecycle: `POST /templates` registers a spec under a content hash, `POST /templates/{id}/deploy` marks it ready to run (instance-create is refused with a 4xx against a merely-registered or undeployed template, verified by the state check in `handleDeployTemplateState`/`handleUndeployTemplateState`), `POST /v1/instances` creates live instances against a deployed template, `POST /templates/{id}/undeploy` retires it (refused with 409 while active instances exist), and `DELETE /templates/{id}` removes it once undeployed and instance-free (refused with 409 while deployed or while active instances remain). An end-to-end test, `TestTemplateLifecycle_FullLifecycleEndToEnd`, drives exactly this sequence — register, pre-deploy instantiate-refused check, deploy, instantiate, delete-while-deployed-refused check, terminate the instance, undeploy, post-undeploy instantiate-refused check, delete the instance, delete the template, and a post-delete 404 — covering every stage the story names.
