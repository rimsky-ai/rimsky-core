---
audit: asset-materialize-endpoint-retired
artifact: decision:asset-materialize-endpoint-retired
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:18Z
---

# No asset-materialize verb exists anywhere on the control surface; re-materialization is message-driven

Supported. Checked all four named surfaces: the control-API route table (`registerAssetsRoutes` in `lib/control/controlapi/assets.go`) registers exactly list/get/versions/materialization-history/delete and no materialize route; the CLI (`cmd/rimsky/cli/asset.go`) exposes exactly `RunAssetList/Show/Versions/Delete/Lineage` and no materialize subcommand; the MCP tool catalog (`lib/control/controlapi/mcp/catalog.go`) registers no asset-related tool at all (materialize or otherwise); and the action-permission table (`lib/control/controlapi/actions.go`) carries exactly `asset:read` and `asset:delete`, no `asset:materialize` row. The only source hits for the string "materialize" are unrelated prose (a proto file comment, a Dockerfile comment, a protocol field comment) — none is a control-surface verb. `test/scenarios/asset_management/asset_management_e2e_test.go::TestStory_AssetManagement_ReMaterializationViaMessage` confirms re-materialization instead flows through an empty instance message that triggers a whole-instance re-run and grows both the lineage and asset-list surfaces.
