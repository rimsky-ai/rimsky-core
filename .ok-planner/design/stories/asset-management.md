---
story: asset-management
status: as-is
---

# Operator manages instance-produced data assets

## Story

As an operator, I can list the data assets a running instance has produced, see the current version of each, walk the version history and materialization audit, retire an asset, and trace its lineage, so that I observe and govern the data outputs nodes produce — including understanding what downstream work consumed an asset before I retire or re-materialize it.

Operator-driven asset observation and governance: list, version, history, retire, lineage trace, through the control-api.

Operators observe and govern data outputs — walking version history, retiring assets — without bouncing the template that produces them; re-materialization is expressed through messages (the empty-message trigger for a whole-instance re-run, or a typed message the template author designs for partial paths).
