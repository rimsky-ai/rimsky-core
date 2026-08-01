---
story: template-lifecycle
status: as-is
---

# Operator manages template catalog

## Story

As an operator, I can register a workflow definition with rimsky, mark it ready to run, create live instances of it, retire it when I don't want new instances, and remove it once nothing's using it, so that I curate the catalog of workflows my stack offers.

Operator-driven template lifecycle: submit, retrieve, deploy, undeploy, instantiate, delete, and pre-flight a template definition through the control-api or CLI.

Operators curate the catalog of workflows their rimsky deployment offers, with a controlled lifecycle that prevents bad templates from producing live instances and prevents in-use templates from being removed.
