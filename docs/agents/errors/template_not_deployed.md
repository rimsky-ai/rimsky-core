---
error: template_not_deployed
surfaced_to: cli-user
---

# Template not deployed

## What it means

`POST /instances` referenced a template that exists in the registry but is in the `registered` (or `undeployed`) state, not `deployed`. Instances can only be created against deployed templates.

## When it happens

When the caller created the instance immediately after registering the template without first deploying it, or against a template that was previously deployed and has since been undeployed.

## What to do

Deploy the template first: `POST /templates/{id}/deploy` (or `rimsky template deploy`). Then retry the instance creation. If the template is intentionally un-deployed, route the instance to a different template.

## See also

- [`../../concepts/template.md`](../../concepts/template.md)
- [`../../concepts/instance.md`](../../concepts/instance.md)
